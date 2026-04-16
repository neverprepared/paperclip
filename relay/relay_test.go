package relay

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ably/ably-go/ably"
	"github.com/mindmorass/paperclip/clipboard"
)

// fakeClipboard is an in-memory clipboardSyncer for tests.
type fakeClipboard struct {
	mu       sync.Mutex
	content  *clipboard.Content
	lastHash string
	writes   []*clipboard.Content
}

func (f *fakeClipboard) Read() (*clipboard.Content, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.content == nil {
		return &clipboard.Content{Type: clipboard.TypeText, Data: []byte("")}, nil
	}
	return f.content, nil
}

func (f *fakeClipboard) Write(c *clipboard.Content) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.content = c
	f.lastHash = c.Hash // mirrors real clipboard.Write behaviour
	f.writes = append(f.writes, c)
	return nil
}

func (f *fakeClipboard) HasChanged(hash string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return hash != f.lastHash
}

func (f *fakeClipboard) SetLastHash(hash string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastHash = hash
}

func (f *fakeClipboard) WriteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes)
}

func (f *fakeClipboard) LastWrite() *clipboard.Content {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) == 0 {
		return nil
	}
	return f.writes[len(f.writes)-1]
}

// buildRelay creates a minimal Relay for handleMessage testing (no Ably connection).
func buildRelay(t *testing.T, room *roomSub, cb *fakeClipboard, sender string, verbose bool) *Relay {
	t.Helper()
	logger := log.New(os.Stderr, "[test] ", 0)
	return &Relay{
		rooms:     []*roomSub{room},
		clipboard: cb,
		logger:    logger,
		verbose:   verbose,
		sender:    sender,
	}
}

// makeAblyMsg creates a valid, encrypted ablyMsg payload as a JSON string.
// tsOverride, when non-zero, replaces the current Unix timestamp (for replay tests).
func makeAblyMsgAt(t *testing.T, room *roomSub, sender string, plaintext []byte, contentType uint8, ts int64) string {
	t.Helper()
	tsBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(tsBytes, uint64(ts))
	payload := append(tsBytes, plaintext...)
	ciphertext, err := encrypt(room.encKey, payload, []byte(room.name))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	msg := ablyMsg{
		Type:   contentType,
		Data:   base64.StdEncoding.EncodeToString(ciphertext),
		Sender: sender,
	}
	msg.MAC = computeMAC(room.encKey, msg)
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(raw)
}

func makeAblyMsg(t *testing.T, room *roomSub, sender string, plaintext []byte, contentType uint8) string {
	t.Helper()
	return makeAblyMsgAt(t, room, sender, plaintext, contentType, time.Now().Unix())
}

func testRoom(passphrase, name string) *roomSub {
	return &roomSub{
		name:   name,
		encKey: deriveKey(passphrase, name),
	}
}

// --- Tests ---

func TestHandleMessage_ValidMessage_WritesClipboard(t *testing.T) {
	room := testRoom("hunter2hunter2", "testroom")
	cb := &fakeClipboard{}
	r := buildRelay(t, room, cb, "self-sender", false)

	plaintext := []byte("hello from relay")
	payload := makeAblyMsg(t, room, "remote-sender", plaintext, uint8(clipboard.TypeText))

	r.handleMessage(room, &ably.Message{Data: payload})

	if cb.WriteCount() != 1 {
		t.Fatalf("expected 1 clipboard write, got %d", cb.WriteCount())
	}
	got := cb.LastWrite()
	if string(got.Data) != string(plaintext) {
		t.Errorf("clipboard data mismatch: got %q, want %q", got.Data, plaintext)
	}
	if got.Type != clipboard.TypeText {
		t.Errorf("expected TypeText, got %v", got.Type)
	}
}

func TestHandleMessage_OwnSender_Ignored(t *testing.T) {
	room := testRoom("hunter2hunter2", "testroom")
	cb := &fakeClipboard{}
	selfSender := "self-123"
	r := buildRelay(t, room, cb, selfSender, false)

	// Message originates from us — should be silently dropped.
	payload := makeAblyMsg(t, room, selfSender, []byte("my own clip"), uint8(clipboard.TypeText))
	r.handleMessage(room, &ably.Message{Data: payload})

	if cb.WriteCount() != 0 {
		t.Errorf("expected no writes for own sender, got %d", cb.WriteCount())
	}
}

func TestHandleMessage_InvalidHMAC_Dropped(t *testing.T) {
	room := testRoom("hunter2hunter2", "testroom")
	cb := &fakeClipboard{}
	r := buildRelay(t, room, cb, "self", false)

	// Build a valid msg then corrupt the MAC (HMAC check runs before decryption).
	tsBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(tsBytes, uint64(time.Now().Unix()))
	payload := append(tsBytes, []byte("attack")...)
	ciphertext, _ := encrypt(room.encKey, payload, []byte(room.name))
	msg := ablyMsg{
		Type:   uint8(clipboard.TypeText),
		Data:   base64.StdEncoding.EncodeToString(ciphertext),
		Sender: "attacker",
		MAC:    "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	}
	raw, _ := json.Marshal(msg)
	r.handleMessage(room, &ably.Message{Data: string(raw)})

	if cb.WriteCount() != 0 {
		t.Errorf("expected no writes for invalid HMAC, got %d", cb.WriteCount())
	}
}

func TestHandleMessage_InvalidBase64_Dropped(t *testing.T) {
	room := testRoom("hunter2hunter2", "testroom")
	cb := &fakeClipboard{}
	r := buildRelay(t, room, cb, "self", false)

	msg := ablyMsg{
		Type:   uint8(clipboard.TypeText),
		Data:   "!!!not-valid-base64!!!",
		Sender: "remote",
	}
	msg.MAC = computeMAC(room.encKey, msg)
	raw, _ := json.Marshal(msg)
	r.handleMessage(room, &ably.Message{Data: string(raw)})

	if cb.WriteCount() != 0 {
		t.Errorf("expected no writes for invalid base64, got %d", cb.WriteCount())
	}
}

func TestHandleMessage_WrongAAD_Dropped(t *testing.T) {
	// Message encrypted for "room-a" arrives on subscription for "room-b".
	roomA := testRoom("hunter2hunter2", "room-a")
	roomB := testRoom("hunter2hunter2", "room-b")
	cb := &fakeClipboard{}
	r := buildRelay(t, roomB, cb, "self", false)

	// Encrypt with room-a's key and AAD.
	ciphertext, _ := encrypt(roomA.encKey, []byte("cross-room data"), []byte(roomA.name))
	// But sign with room-b's key (so MAC passes) and hand to room-b handler.
	msg := ablyMsg{
		Type:   uint8(clipboard.TypeText),
		Data:   base64.StdEncoding.EncodeToString(ciphertext),
		Sender: "remote",
	}
	msg.MAC = computeMAC(roomB.encKey, msg)
	raw, _ := json.Marshal(msg)
	r.handleMessage(roomB, &ably.Message{Data: string(raw)})

	if cb.WriteCount() != 0 {
		t.Errorf("expected no writes for cross-room AAD mismatch, got %d", cb.WriteCount())
	}
}

func TestHandleMessage_TamperedCiphertext_Dropped(t *testing.T) {
	room := testRoom("hunter2hunter2", "testroom")
	cb := &fakeClipboard{}
	r := buildRelay(t, room, cb, "self", false)

	// Build a properly timestamped payload then tamper with it after encryption.
	tsBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(tsBytes, uint64(time.Now().Unix()))
	payload := append(tsBytes, []byte("original")...)
	ciphertext, _ := encrypt(room.encKey, payload, []byte(room.name))
	// Flip a byte in the ciphertext body (past the 12-byte nonce).
	ciphertext[12] ^= 0xFF
	msg := ablyMsg{
		Type:   uint8(clipboard.TypeText),
		Data:   base64.StdEncoding.EncodeToString(ciphertext),
		Sender: "remote",
	}
	msg.MAC = computeMAC(room.encKey, msg)
	raw, _ := json.Marshal(msg)
	r.handleMessage(room, &ably.Message{Data: string(raw)})

	if cb.WriteCount() != 0 {
		t.Errorf("expected no writes for tampered ciphertext, got %d", cb.WriteCount())
	}
}

func TestHandleMessage_NonJSONData_Dropped(t *testing.T) {
	room := testRoom("hunter2hunter2", "testroom")
	cb := &fakeClipboard{}
	r := buildRelay(t, room, cb, "self", false)

	r.handleMessage(room, &ably.Message{Data: "not json at all"})

	if cb.WriteCount() != 0 {
		t.Errorf("expected no writes for non-JSON data, got %d", cb.WriteCount())
	}
}

func TestHandleMessage_NonStringData_Dropped(t *testing.T) {
	room := testRoom("hunter2hunter2", "testroom")
	cb := &fakeClipboard{}
	r := buildRelay(t, room, cb, "self", false)

	// Ably sometimes passes non-string types.
	r.handleMessage(room, &ably.Message{Data: 12345})

	if cb.WriteCount() != 0 {
		t.Errorf("expected no writes for non-string message data, got %d", cb.WriteCount())
	}
}

func TestHandleMessage_HashSetAfterWrite(t *testing.T) {
	// After receiving content via relay, the local lastHash must be updated so
	// the next poll cycle does not re-publish the same content.
	room := testRoom("hunter2hunter2", "testroom")
	cb := &fakeClipboard{}
	r := buildRelay(t, room, cb, "self", false)

	plaintext := []byte("received content")
	payload := makeAblyMsg(t, room, "remote", plaintext, uint8(clipboard.TypeText))
	r.handleMessage(room, &ably.Message{Data: payload})

	expectedHash := plaintextHash(plaintext)
	got := cb.LastWrite()
	if got == nil {
		t.Fatal("expected a clipboard write")
	}
	if got.Hash != expectedHash {
		t.Errorf("hash mismatch: got %q, want %q", got.Hash, expectedHash)
	}
	// HasChanged should now return false for the same hash (echo suppression).
	if cb.HasChanged(expectedHash) {
		t.Error("HasChanged returned true after receiving content — would cause re-publish loop")
	}
}

func TestHandleMessage_ImageType_PreservedOnWrite(t *testing.T) {
	room := testRoom("hunter2hunter2", "testroom")
	cb := &fakeClipboard{}
	r := buildRelay(t, room, cb, "self", false)

	plaintext := []byte{0x89, 0x50, 0x4E, 0x47} // PNG header bytes
	payload := makeAblyMsg(t, room, "remote", plaintext, uint8(clipboard.TypeImage))
	r.handleMessage(room, &ably.Message{Data: payload})

	got := cb.LastWrite()
	if got == nil {
		t.Fatal("expected a clipboard write")
	}
	if got.Type != clipboard.TypeImage {
		t.Errorf("expected TypeImage, got %v", got.Type)
	}
}

func TestHandleMessage_OldTimestamp_Dropped(t *testing.T) {
	room := testRoom("hunter2hunter2", "testroom")
	cb := &fakeClipboard{}
	r := buildRelay(t, room, cb, "self", false)

	// Timestamp is 10 minutes in the past — outside the ±5-minute window.
	oldTs := time.Now().Unix() - 600
	payload := makeAblyMsgAt(t, room, "remote", []byte("stale clip"), uint8(clipboard.TypeText), oldTs)
	r.handleMessage(room, &ably.Message{Data: payload})

	if cb.WriteCount() != 0 {
		t.Errorf("expected no writes for old timestamp, got %d", cb.WriteCount())
	}
}

func TestHandleMessage_FutureTimestamp_Dropped(t *testing.T) {
	room := testRoom("hunter2hunter2", "testroom")
	cb := &fakeClipboard{}
	r := buildRelay(t, room, cb, "self", false)

	// Timestamp is 10 minutes in the future — outside the ±5-minute window.
	futureTs := time.Now().Unix() + 600
	payload := makeAblyMsgAt(t, room, "remote", []byte("future clip"), uint8(clipboard.TypeText), futureTs)
	r.handleMessage(room, &ably.Message{Data: payload})

	if cb.WriteCount() != 0 {
		t.Errorf("expected no writes for future timestamp, got %d", cb.WriteCount())
	}
}

func TestHandleMessage_TimestampAtWindowEdge_Accepted(t *testing.T) {
	room := testRoom("hunter2hunter2", "testroom")
	cb := &fakeClipboard{}
	r := buildRelay(t, room, cb, "self", false)

	// Timestamp is exactly at the edge of the window (4m 59s old).
	edgeTs := time.Now().Unix() - (replayWindowSeconds - 1)
	payload := makeAblyMsgAt(t, room, "remote", []byte("edge clip"), uint8(clipboard.TypeText), edgeTs)
	r.handleMessage(room, &ably.Message{Data: payload})

	if cb.WriteCount() != 1 {
		t.Errorf("expected 1 write for timestamp at window edge, got %d", cb.WriteCount())
	}
}
