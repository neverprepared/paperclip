package relay

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/ably/ably-go/ably"
	"github.com/mindmorass/paperclip/clipboard"
)

const (
	replayWindowSeconds = 5 * 60 // ±5 minutes

	// ablyMessageSizeLimit is the hard maximum message size enforced by Ably.
	// Payloads larger than this are silently dropped by the broker.
	ablyMessageSizeLimit = 64 * 1024 // 64 KB

	// maxPlaintextBytes is the largest plaintext that can be published and still
	// fit within ablyMessageSizeLimit after:
	//   encrypt: +12-byte nonce +16-byte GCM tag +8-byte timestamp = +36 bytes
	//   base64:  *4/3
	//   JSON envelope (t, d, s, m fields): ~200 bytes overhead
	// Conservative limit: 47 KB leaves ~1 KB headroom.
	maxPlaintextBytes = 47 * 1024
)

// ClipboardStatus represents the state of a single relay room.
type ClipboardStatus struct {
	Name      string
	Connected bool
	Encrypted bool
}

// ablyMsg is the typed wire format for messages published to Ably channels.
// Hash is intentionally omitted: it would expose content identity (same
// clipboard → same hash) to anyone monitoring the Ably channel. Echo
// prevention uses sender ID instead.
type ablyMsg struct {
	Type   uint8  `json:"t"`
	Data   string `json:"d"` // base64(AES-256-GCM ciphertext)
	Sender string `json:"s"` // random per-session ID
	MAC    string `json:"m"` // HMAC-SHA256(encKey, t:d:s) hex-encoded
}

// clipboardSyncer abstracts clipboard operations so the relay is testable
// without touching the real OS clipboard.
type clipboardSyncer interface {
	Read() (*clipboard.Content, error)
	Write(*clipboard.Content) error
	HasChanged(string) bool
	SetLastHash(string)
}

// Relay syncs clipboard data through Ably pub/sub across multiple rooms.
type Relay struct {
	client    *ably.Realtime
	rooms     []*roomSub
	clipboard clipboardSyncer
	logger    *log.Logger
	verbose   bool
	sender    string

	ctx      context.Context
	cancel   context.CancelFunc
	stopChan chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	syncMu     sync.Mutex
	lastSyncAt time.Time

	filterMu      sync.RWMutex
	publishFilter map[string]bool // nil = publish to all; non-nil = hub mode with selected targets
}

// SetPublishFilter sets which clipboards this relay publishes to.
// An empty/nil slice means publish to all (spoke behaviour).
// A non-empty slice enables hub mode, publishing only to named clipboards.
func (r *Relay) SetPublishFilter(targets []string) {
	r.filterMu.Lock()
	defer r.filterMu.Unlock()
	if len(targets) == 0 {
		r.publishFilter = nil
		return
	}
	r.publishFilter = make(map[string]bool, len(targets))
	for _, t := range targets {
		r.publishFilter[t] = true
	}
}

func (r *Relay) shouldPublishTo(name string) bool {
	r.filterMu.RLock()
	defer r.filterMu.RUnlock()
	if r.publishFilter == nil {
		return true
	}
	return r.publishFilter[name]
}

// LastSyncAt returns the time of the most recent successful sync (send or receive).
// Returns zero time if no sync has occurred yet.
func (r *Relay) LastSyncAt() time.Time {
	r.syncMu.Lock()
	defer r.syncMu.Unlock()
	return r.lastSyncAt
}

func (r *Relay) recordSync() {
	r.syncMu.Lock()
	r.lastSyncAt = time.Now()
	r.syncMu.Unlock()
}

type roomSub struct {
	name    string
	channel *ably.RealtimeChannel
	encKey  []byte // AES-256-GCM key derived from passphrase
}

// New creates a new Ably relay connected to multiple rooms.
// All rooms must have a passphrase in the system keychain — rooms without one
// are skipped. Returns an error if no rooms have passphrases.
// cb accepts any clipboardSyncer implementation; pass a *clipboard.Clipboard
// for production use or a test double in unit tests.
func New(apiKey string, roomNames []string, cb clipboardSyncer, logger *log.Logger, verbose bool) (*Relay, error) {
	if verbose {
		logger.Printf("Ably key: [configured]")
		logger.Printf("Ably clipboards: %v", roomNames)
	}

	client, err := ably.NewRealtime(
		ably.WithKey(apiKey),
		ably.WithAutoConnect(true),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Ably client: %w", err)
	}

	var rooms []*roomSub
	for _, name := range roomNames {
		room := &roomSub{
			name:    name,
			channel: client.Channels.Get(name),
		}

		// Passphrase is required — skip rooms without one.
		if passphrase, err := GetPassphrase(name); err == nil && passphrase != "" {
			room.encKey = deriveKey(passphrase, name)
			logger.Printf("Encryption enabled for clipboard '%s'", name)
			rooms = append(rooms, room)
		} else if err != nil {
			// Distinguish a keychain access failure (locked keychain, permission
			// denied, etc.) from a genuinely unconfigured passphrase so users can
			// diagnose the problem.
			logger.Printf("WARNING: keychain error reading passphrase for clipboard '%s': %v — skipping (unlock your keychain or re-enter the passphrase via the tray)", name, err)
		} else {
			logger.Printf("WARNING: empty passphrase for clipboard '%s' — skipping (set a passphrase via the tray)", name)
		}
	}

	if len(rooms) == 0 {
		client.Close()
		return nil, fmt.Errorf("no clipboards with passphrases configured — encryption is required")
	}

	senderBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, senderBytes); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to generate sender ID: %w", err)
	}
	sender := hex.EncodeToString(senderBytes)

	ctx, cancel := context.WithCancel(context.Background())

	return &Relay{
		client:    client,
		rooms:     rooms,
		clipboard: cb,
		logger:    logger,
		verbose:   verbose,
		sender:    sender,
		ctx:       ctx,
		cancel:    cancel,
		stopChan:  make(chan struct{}),
	}, nil
}

// Start begins subscribing to all rooms and publishing clipboard changes.
// Returns an error if pollMs is not positive or if any Ably subscription fails.
// On failure the relay context is cancelled to clean up any partially-established
// subscriptions; callers should not use the Relay after Start returns an error.
func (r *Relay) Start(pollMs int) error {
	if pollMs <= 0 {
		return fmt.Errorf("poll interval must be positive, got %d ms", pollMs)
	}

	for _, room := range r.rooms {
		rm := room // capture for closure
		_, err := room.channel.SubscribeAll(r.ctx, func(msg *ably.Message) {
			r.handleMessage(rm, msg)
		})
		if err != nil {
			// Cancel the context to tear down any subscriptions already established
			// for earlier rooms in this loop, preventing a goroutine leak.
			r.cancel()
			return fmt.Errorf("failed to subscribe to clipboard %s: %w", room.name, err)
		}
		r.logger.Printf("Ably relay connected (clipboard: %s)", room.name)
	}

	r.wg.Add(1)
	go r.pollAndPublish(time.Duration(pollMs) * time.Millisecond)

	return nil
}

// Stop shuts down the relay and waits for background goroutines to exit.
// Safe to call multiple times; subsequent calls are no-ops.
func (r *Relay) Stop() {
	r.stopOnce.Do(func() {
		r.cancel()
		close(r.stopChan)
		r.wg.Wait()
		r.client.Close()
	})
}

// Connected returns whether the Ably connection is active.
func (r *Relay) Connected() bool {
	return r.client.Connection.State() == ably.ConnectionStateConnected
}

// Status returns the status of each room.
func (r *Relay) Status() []ClipboardStatus {
	connected := r.Connected()
	statuses := make([]ClipboardStatus, len(r.rooms))
	for i, room := range r.rooms {
		statuses[i] = ClipboardStatus{
			Name:      room.name,
			Connected: connected,
			Encrypted: room.encKey != nil,
		}
	}
	return statuses
}

// ClipboardNames returns the names of all rooms.
func (r *Relay) ClipboardNames() []string {
	names := make([]string, len(r.rooms))
	for i, room := range r.rooms {
		names[i] = room.name
	}
	return names
}

func (r *Relay) handleMessage(room *roomSub, msg *ably.Message) {
	rawJSON, ok := msg.Data.(string)
	if !ok {
		return
	}

	var amsg ablyMsg
	if err := json.Unmarshal([]byte(rawJSON), &amsg); err != nil {
		return
	}

	// Ignore our own messages.
	if amsg.Sender == r.sender {
		return
	}

	// Verify HMAC — rejects injected messages from parties without the key.
	if room.encKey == nil {
		r.logger.Printf("ERROR: received message for clipboard '%s' with no encryption key — dropping", room.name)
		return
	}
	if !verifyMAC(room.encKey, amsg) {
		r.logger.Printf("HMAC verification failed for clipboard '%s' (sender %s) — dropping message", room.name, amsg.Sender)
		return
	}

	raw, err := base64.StdEncoding.DecodeString(amsg.Data)
	if err != nil {
		r.logger.Printf("Failed to decode relay message: %v", err)
		return
	}

	// Decrypt — room name is AAD to prevent cross-room replay.
	decrypted, err := decrypt(room.encKey, raw, []byte(room.name))
	if err != nil {
		r.logger.Printf("Failed to decrypt message from clipboard '%s': %v", room.name, err)
		return
	}

	// Extract and validate the 8-byte timestamp prepended by the sender.
	if len(decrypted) < 8 {
		r.logger.Printf("Decrypted payload too short from clipboard '%s' — dropping", room.name)
		return
	}
	msgTs := int64(binary.BigEndian.Uint64(decrypted[:8]))
	plaintext := decrypted[8:]

	delta := time.Now().Unix() - msgTs
	if delta < 0 {
		delta = -delta
	}
	if delta > replayWindowSeconds {
		r.logger.Printf("Replay rejected for clipboard '%s': message timestamp drift %ds exceeds %ds window", room.name, delta, replayWindowSeconds)
		return
	}

	// Compute local hash so clipboard.Write sets the correct lastHash.
	// This prevents re-publishing received content on the next poll cycle.
	localHash := plaintextHash(plaintext)
	content := &clipboard.Content{
		Type: clipboard.ContentType(amsg.Type),
		Data: plaintext,
		Hash: localHash,
	}

	if err := r.clipboard.Write(content); err != nil {
		r.logger.Printf("Failed to write clipboard from relay: %v", err)
		return
	}

	r.recordSync()

	if r.verbose {
		typeStr := "text"
		if content.Type == clipboard.TypeImage {
			typeStr = "image"
		}
		r.logger.Printf("Received %s (%d bytes) via clipboard '%s' (encrypted)", typeStr, len(plaintext), room.name)
	}
}

func (r *Relay) pollAndPublish(interval time.Duration) {
	defer r.wg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopChan:
			return
		case <-ticker.C:
			content, err := r.clipboard.Read()
			if err != nil {
				continue
			}

			if !r.clipboard.HasChanged(content.Hash) {
				continue
			}

			r.clipboard.SetLastHash(content.Hash)

			// Publish to selected clipboards (all in spoke mode; filtered in hub mode).
			for _, room := range r.rooms {
				if !r.shouldPublishTo(room.name) {
					continue
				}
				// Encrypt — mandatory, refuse to publish if no key.
				if room.encKey == nil {
					r.logger.Printf("ERROR: clipboard '%s' has no encryption key — refusing to publish", room.name)
					continue
				}

				// Enforce Ably's 64 KB message limit early, before doing
				// encryption work.  base64(nonce+ts+data+gcm) + JSON overhead
				// means the usable plaintext limit is ~47 KB.
				if len(content.Data) > maxPlaintextBytes {
					r.logger.Printf("WARNING: clipboard payload too large for clipboard '%s' (%d bytes, limit %d) — dropping", room.name, len(content.Data), maxPlaintextBytes)
					continue
				}

				// Prepend 8-byte big-endian Unix timestamp inside the
				// AEAD envelope so receivers can reject replayed messages.
				ts := make([]byte, 8)
				binary.BigEndian.PutUint64(ts, uint64(time.Now().Unix()))
				payload := append(ts, content.Data...)

				// Room name as AAD binds ciphertext to this room.
				ciphertext, err := encrypt(room.encKey, payload, []byte(room.name))
				if err != nil {
					r.logger.Printf("Failed to encrypt for clipboard '%s': %v", room.name, err)
					continue
				}

				amsg := ablyMsg{
					Type:   uint8(content.Type),
					Data:   base64.StdEncoding.EncodeToString(ciphertext),
					Sender: r.sender,
				}
				amsg.MAC = computeMAC(room.encKey, amsg)

				msgJSON, err := json.Marshal(amsg)
				if err != nil {
					r.logger.Printf("Failed to marshal message for clipboard '%s': %v", room.name, err)
					continue
				}

				// Final wire-size safety net: the serialised JSON must fit within
				// Ably's hard limit.  Under normal circumstances the plaintext
				// guard above prevents reaching here with an oversized payload;
				// this catches any unexpected overhead (e.g. very long room names).
				if len(msgJSON) > ablyMessageSizeLimit {
					r.logger.Printf("WARNING: serialised message too large for clipboard '%s' (%d bytes, Ably limit %d) — dropping", room.name, len(msgJSON), ablyMessageSizeLimit)
					continue
				}

				err = room.channel.Publish(r.ctx, "clipboard", string(msgJSON))
				if err != nil {
					r.logger.Printf("Failed to publish to clipboard %s: %v", room.name, err)
				} else {
					r.recordSync()
				}
				if err == nil && r.verbose {
					typeStr := "text"
					if content.Type == clipboard.TypeImage {
						typeStr = "image"
					}
					r.logger.Printf("Published %s (%d bytes) to clipboard '%s' (encrypted)", typeStr, len(content.Data), room.name)
				}
			}
		}
	}
}

// plaintextHash returns the SHA-256 hex digest of data, matching the hash
// scheme used by the clipboard package so SetLastHash stays consistent.
func plaintextHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// computeMAC returns HMAC-SHA256(key, "t:d:s") as a hex string.
// The MAC authenticates all message fields so injected messages are rejected.
func computeMAC(key []byte, msg ablyMsg) string {
	h := hmac.New(sha256.New, key)
	fmt.Fprintf(h, "%d:%s:%s", msg.Type, msg.Data, msg.Sender)
	return hex.EncodeToString(h.Sum(nil))
}

// verifyMAC checks the MAC field of an incoming message.
func verifyMAC(key []byte, msg ablyMsg) bool {
	expected := computeMAC(key, msg)
	return hmac.Equal([]byte(expected), []byte(msg.MAC))
}
