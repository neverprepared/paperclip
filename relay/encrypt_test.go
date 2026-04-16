package relay

import (
	"bytes"
	"testing"
)

// testKey returns a fixed 32-byte AES key derived from a known passphrase.
func testKey(t *testing.T) []byte {
	t.Helper()
	return deriveKey("test-passphrase", "test-room")
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := testKey(t)
	plaintext := []byte("hello clipboard")
	aad := []byte("test-room")

	ciphertext, err := encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	got, err := decrypt(key, ciphertext, aad)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if !bytes.Equal(got, plaintext) {
		t.Errorf("round-trip mismatch: got %q, want %q", got, plaintext)
	}
}

func TestEncryptDecryptEmptyPlaintext(t *testing.T) {
	key := testKey(t)
	aad := []byte("room")

	ciphertext, err := encrypt(key, []byte{}, aad)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	got, err := decrypt(key, ciphertext, aad)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if !bytes.Equal(got, []byte{}) {
		t.Errorf("expected empty plaintext, got %q", got)
	}
}

func TestEncryptDecryptLargePayload(t *testing.T) {
	key := testKey(t)
	aad := []byte("room")
	plaintext := bytes.Repeat([]byte("A"), 10*1024*1024) // 10 MB

	ciphertext, err := encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	got, err := decrypt(key, ciphertext, aad)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if !bytes.Equal(got, plaintext) {
		t.Error("large payload round-trip mismatch")
	}
}

func TestEncryptAADMismatchFails(t *testing.T) {
	key := testKey(t)
	plaintext := []byte("secret data")

	ciphertext, err := encrypt(key, plaintext, []byte("room-a"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Decrypting with a different AAD must fail — prevents cross-room replay.
	_, err = decrypt(key, ciphertext, []byte("room-b"))
	if err == nil {
		t.Error("expected decryption to fail with wrong AAD, but it succeeded")
	}
}

func TestEncryptWrongKeyFails(t *testing.T) {
	key1 := deriveKey("passphrase-a", "room")
	key2 := deriveKey("passphrase-b", "room")
	aad := []byte("room")

	ciphertext, err := encrypt(key1, []byte("data"), aad)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	_, err = decrypt(key2, ciphertext, aad)
	if err == nil {
		t.Error("expected decryption to fail with wrong key, but it succeeded")
	}
}

func TestEncryptProducesUniqueNonces(t *testing.T) {
	key := testKey(t)
	plaintext := []byte("same content")
	aad := []byte("room")

	c1, err := encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt 1: %v", err)
	}
	c2, err := encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt 2: %v", err)
	}

	if bytes.Equal(c1, c2) {
		t.Error("two encryptions of the same plaintext produced identical ciphertexts (nonce reuse)")
	}
}

func TestDecryptTooShortFails(t *testing.T) {
	key := testKey(t)

	_, err := decrypt(key, []byte{0x01, 0x02}, []byte("room"))
	if err == nil {
		t.Error("expected error for too-short ciphertext, got nil")
	}
}

func TestDecryptEmptyFails(t *testing.T) {
	key := testKey(t)

	_, err := decrypt(key, []byte{}, []byte("room"))
	if err == nil {
		t.Error("expected error for empty ciphertext, got nil")
	}
}

func TestDecryptTamperedCiphertextFails(t *testing.T) {
	key := testKey(t)
	aad := []byte("room")

	ciphertext, err := encrypt(key, []byte("original"), aad)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Flip a byte in the ciphertext body (after the nonce).
	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[len(tampered)-1] ^= 0xFF

	_, err = decrypt(key, tampered, aad)
	if err == nil {
		t.Error("expected decryption to fail for tampered ciphertext, but it succeeded")
	}
}

func TestDeriveKeyDeterministic(t *testing.T) {
	k1 := deriveKey("my-passphrase", "my-room")
	k2 := deriveKey("my-passphrase", "my-room")

	if !bytes.Equal(k1, k2) {
		t.Error("deriveKey is not deterministic for the same inputs")
	}
}

func TestDeriveKeyLength(t *testing.T) {
	k := deriveKey("passphrase", "room")
	if len(k) != 32 {
		t.Errorf("expected 32-byte key (AES-256), got %d bytes", len(k))
	}
}

func TestDeriveKeyRoomIsolation(t *testing.T) {
	// Same passphrase, different rooms must produce different keys.
	k1 := deriveKey("shared-passphrase", "room-a")
	k2 := deriveKey("shared-passphrase", "room-b")

	if bytes.Equal(k1, k2) {
		t.Error("different rooms with the same passphrase produced the same key")
	}
}

func TestDeriveKeyPassphraseIsolation(t *testing.T) {
	// Different passphrases, same room must produce different keys.
	k1 := deriveKey("passphrase-x", "room")
	k2 := deriveKey("passphrase-y", "room")

	if bytes.Equal(k1, k2) {
		t.Error("different passphrases for the same room produced the same key")
	}
}
