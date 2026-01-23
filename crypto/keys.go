package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/flynn/noise"
)

const (
	keyFilePerms = 0600
	keySize      = 32
)

// GetConfigDir returns the OS-appropriate config directory for paperclip
func GetConfigDir() (string, error) {
	var baseDir string

	switch runtime.GOOS {
	case "windows":
		baseDir = os.Getenv("APPDATA")
		if baseDir == "" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("cannot determine home directory: %w", err)
			}
			baseDir = filepath.Join(homeDir, "AppData", "Roaming")
		}
	default: // macOS, Linux
		baseDir = os.Getenv("XDG_CONFIG_HOME")
		if baseDir == "" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("cannot determine home directory: %w", err)
			}
			baseDir = filepath.Join(homeDir, ".config")
		}
	}

	return filepath.Join(baseDir, "paperclip"), nil
}

// GenerateKeypair creates a new Curve25519 keypair for Noise protocol
func GenerateKeypair() (noise.DHKey, error) {
	dh := noise.DH25519
	return dh.GenerateKeypair(rand.Reader)
}

// LoadOrCreateIdentity loads the identity keypair from the config directory,
// or creates a new one if it doesn't exist
func LoadOrCreateIdentity(configDir string) (noise.DHKey, error) {
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return noise.DHKey{}, fmt.Errorf("cannot create config directory: %w", err)
	}

	keyPath := filepath.Join(configDir, "identity.key")

	// Try to load existing key
	data, err := os.ReadFile(keyPath)
	if err == nil {
		if len(data) != keySize*2 {
			return noise.DHKey{}, fmt.Errorf("invalid identity.key size: expected %d, got %d", keySize*2, len(data))
		}
		return noise.DHKey{
			Private: data[:keySize],
			Public:  data[keySize:],
		}, nil
	}

	if !os.IsNotExist(err) {
		return noise.DHKey{}, fmt.Errorf("cannot read identity.key: %w", err)
	}

	// Generate new keypair
	key, err := GenerateKeypair()
	if err != nil {
		return noise.DHKey{}, fmt.Errorf("cannot generate keypair: %w", err)
	}

	// Save to file
	if err := SaveIdentity(keyPath, key); err != nil {
		return noise.DHKey{}, err
	}

	return key, nil
}

// SaveIdentity writes the keypair to a file with secure permissions
func SaveIdentity(path string, key noise.DHKey) error {
	data := make([]byte, keySize*2)
	copy(data[:keySize], key.Private)
	copy(data[keySize:], key.Public)

	if err := os.WriteFile(path, data, keyFilePerms); err != nil {
		return fmt.Errorf("cannot write identity.key: %w", err)
	}

	return nil
}

// PublicKeyFingerprint returns a short base64 fingerprint of a public key
func PublicKeyFingerprint(pubKey []byte) string {
	if len(pubKey) < 8 {
		return base64.StdEncoding.EncodeToString(pubKey)
	}
	// Show first 12 chars of base64 (8 bytes worth)
	return base64.StdEncoding.EncodeToString(pubKey[:8])[:12]
}

// PublicKeyFull returns the full base64 encoding of a public key
func PublicKeyFull(pubKey []byte) string {
	return base64.StdEncoding.EncodeToString(pubKey)
}
