package relay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ably/ably-go/ably"
	"github.com/mindmorass/paperclip/clipboard"
)

// RoomStatus represents the state of a single relay room.
type RoomStatus struct {
	Name      string
	Connected bool
	Encrypted bool
}

// ablyMsg is the typed wire format for messages published to Ably channels.
type ablyMsg struct {
	Type   uint8  `json:"t"`
	Data   string `json:"d"` // base64(AES-256-GCM ciphertext)
	Hash   string `json:"h"` // hash of plaintext for echo prevention
	Sender string `json:"s"` // random per-session ID
	MAC    string `json:"m"` // HMAC-SHA256(encKey, t:d:h:s) hex-encoded
}

// Relay syncs clipboard data through Ably pub/sub across multiple rooms.
type Relay struct {
	client    *ably.Realtime
	rooms     []*roomSub
	clipboard *clipboard.Clipboard
	logger    *log.Logger
	verbose   bool
	sender    string

	// Echo prevention
	lastPublishedHash string
	mu                sync.Mutex

	ctx      context.Context
	cancel   context.CancelFunc
	stopChan chan struct{}
	wg       sync.WaitGroup
}

type roomSub struct {
	name   string
	channel *ably.RealtimeChannel
	encKey []byte // AES-256-GCM key derived from passphrase
}

// New creates a new Ably relay connected to multiple rooms.
// All rooms must have a passphrase in the system keychain — rooms without one
// are skipped. Returns an error if no rooms have passphrases.
func New(apiKey string, roomNames []string, cb *clipboard.Clipboard, logger *log.Logger, verbose bool) (*Relay, error) {
	if verbose {
		keyPreview := apiKey
		if len(keyPreview) > 20 {
			keyPreview = keyPreview[:20] + "..."
		}
		logger.Printf("Ably key: %s", keyPreview)
		logger.Printf("Ably rooms: %v", roomNames)
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
			logger.Printf("Encryption enabled for room '%s'", name)
			rooms = append(rooms, room)
		} else {
			logger.Printf("WARNING: No passphrase for room '%s' — skipping (encryption is required)", name)
		}
	}

	if len(rooms) == 0 {
		client.Close()
		return nil, fmt.Errorf("no rooms with passphrases configured — encryption is required")
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Relay{
		client:    client,
		rooms:     rooms,
		clipboard: cb,
		logger:    logger,
		verbose:   verbose,
		sender:    fmt.Sprintf("%d", time.Now().UnixNano()%100000),
		ctx:       ctx,
		cancel:    cancel,
		stopChan:  make(chan struct{}),
	}, nil
}

// Start begins subscribing to all rooms and publishing clipboard changes.
func (r *Relay) Start(pollMs int) error {
	for _, room := range r.rooms {
		rm := room // capture for closure
		_, err := room.channel.SubscribeAll(r.ctx, func(msg *ably.Message) {
			r.handleMessage(rm, msg)
		})
		if err != nil {
			return fmt.Errorf("failed to subscribe to room %s: %w", room.name, err)
		}
		r.logger.Printf("Ably relay connected (room: %s)", room.name)
	}

	r.wg.Add(1)
	go r.pollAndPublish(time.Duration(pollMs) * time.Millisecond)

	return nil
}

// Stop shuts down the relay and waits for background goroutines to exit.
func (r *Relay) Stop() {
	r.cancel()
	close(r.stopChan)
	r.wg.Wait()
	r.client.Close()
}

// Connected returns whether the Ably connection is active.
func (r *Relay) Connected() bool {
	return r.client.Connection.State() == ably.ConnectionStateConnected
}

// Status returns the status of each room.
func (r *Relay) Status() []RoomStatus {
	connected := r.Connected()
	statuses := make([]RoomStatus, len(r.rooms))
	for i, room := range r.rooms {
		statuses[i] = RoomStatus{
			Name:      room.name,
			Connected: connected,
			Encrypted: room.encKey != nil,
		}
	}
	return statuses
}

// RoomNames returns the names of all rooms.
func (r *Relay) RoomNames() []string {
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

	// Echo prevention.
	r.mu.Lock()
	if amsg.Hash == r.lastPublishedHash {
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()

	// Verify HMAC — rejects injected messages from parties without the key.
	if room.encKey == nil {
		r.logger.Printf("ERROR: received message for room '%s' with no encryption key — dropping", room.name)
		return
	}
	if !verifyMAC(room.encKey, amsg) {
		r.logger.Printf("HMAC verification failed for room '%s' — dropping message", room.name)
		return
	}

	raw, err := base64.StdEncoding.DecodeString(amsg.Data)
	if err != nil {
		r.logger.Printf("Failed to decode relay message: %v", err)
		return
	}

	// Decrypt — room name is AAD to prevent cross-room replay.
	plaintext, err := decrypt(room.encKey, raw, []byte(room.name))
	if err != nil {
		r.logger.Printf("Failed to decrypt message from room '%s': %v", room.name, err)
		return
	}

	content := &clipboard.Content{
		Type: clipboard.ContentType(amsg.Type),
		Data: plaintext,
		Hash: amsg.Hash,
	}

	if err := r.clipboard.Write(content); err != nil {
		r.logger.Printf("Failed to write clipboard from relay: %v", err)
		return
	}

	r.clipboard.SetLastHash(amsg.Hash)

	if r.verbose {
		typeStr := "text"
		if content.Type == clipboard.TypeImage {
			typeStr = "image"
		}
		r.logger.Printf("Received %s (%d bytes) via room '%s' (encrypted)", typeStr, len(plaintext), room.name)
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

			r.mu.Lock()
			r.lastPublishedHash = content.Hash
			r.mu.Unlock()

			// Publish to all rooms.
			for _, room := range r.rooms {
				// Encrypt — mandatory, refuse to publish if no key.
				if room.encKey == nil {
					r.logger.Printf("ERROR: room '%s' has no encryption key — refusing to publish", room.name)
					continue
				}

				// Room name as AAD binds ciphertext to this room.
				ciphertext, err := encrypt(room.encKey, content.Data, []byte(room.name))
				if err != nil {
					r.logger.Printf("Failed to encrypt for room '%s': %v", room.name, err)
					continue
				}

				amsg := ablyMsg{
					Type:   uint8(content.Type),
					Data:   base64.StdEncoding.EncodeToString(ciphertext),
					Hash:   content.Hash,
					Sender: r.sender,
				}
				amsg.MAC = computeMAC(room.encKey, amsg)

				payload, err := json.Marshal(amsg)
				if err != nil {
					r.logger.Printf("Failed to marshal message for room '%s': %v", room.name, err)
					continue
				}

				err = room.channel.Publish(r.ctx, "clipboard", string(payload))
				if err != nil {
					r.logger.Printf("Failed to publish to room %s: %v", room.name, err)
				} else if r.verbose {
					typeStr := "text"
					if content.Type == clipboard.TypeImage {
						typeStr = "image"
					}
					r.logger.Printf("Published %s (%d bytes) to room '%s' (encrypted)", typeStr, len(content.Data), room.name)
				}
			}
		}
	}
}

// computeMAC returns HMAC-SHA256(key, "t:d:h:s") as a hex string.
// The MAC authenticates all message fields so injected messages are rejected.
func computeMAC(key []byte, msg ablyMsg) string {
	h := hmac.New(sha256.New, key)
	fmt.Fprintf(h, "%d:%s:%s:%s", msg.Type, msg.Data, msg.Hash, msg.Sender)
	return hex.EncodeToString(h.Sum(nil))
}

// verifyMAC checks the MAC field of an incoming message.
func verifyMAC(key []byte, msg ablyMsg) bool {
	expected := computeMAC(key, msg)
	return hmac.Equal([]byte(expected), []byte(msg.MAC))
}
