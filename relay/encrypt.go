package relay

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// deriveKey derives a 256-bit AES key from a passphrase and room name.
// The room name is used as a salt so different rooms get different keys
// even with the same passphrase.
func deriveKey(passphrase, room string) []byte {
	salt := sha256.Sum256([]byte("paperclip:" + room))
	// Argon2id parameters: t=2, m=64MB, p=4 — meets RFC 9106 interactive minimum.
	return argon2.IDKey([]byte(passphrase), salt[:], 2, 64*1024, 4, 32)
}

// encrypt encrypts plaintext using AES-256-GCM with the given key.
// aad is included as additional authenticated data (e.g. room name) to bind
// ciphertexts to a specific context and prevent cross-room replay.
// Returns nonce + ciphertext.
func encrypt(key, plaintext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	return gcm.Seal(nonce, nonce, plaintext, aad), nil
}

// decrypt decrypts data produced by encrypt (nonce + ciphertext).
// aad must match the value used during encryption.
func decrypt(key, data, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, aad)
}
