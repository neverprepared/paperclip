package relay

import (
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	keychainService  = "com.github.mindmorass.paperclip"
	keychainAPIKey   = "ably-api-key"
	minPassphraseLen = 8
)

// SetPassphrase stores a room's passphrase in the system keychain.
// Returns an error if the passphrase is shorter than minPassphraseLen.
func SetPassphrase(name, passphrase string) error {
	if len(passphrase) < minPassphraseLen {
		return fmt.Errorf("passphrase must be at least %d characters", minPassphraseLen)
	}
	return keyring.Set(keychainService, "clipboard:"+name, passphrase)
}

// GetPassphrase retrieves a room's passphrase from the system keychain.
func GetPassphrase(name string) (string, error) {
	pass, err := keyring.Get(keychainService, "clipboard:"+name)
	if err != nil {
		return "", fmt.Errorf("no passphrase found for clipboard '%s': %w", name, err)
	}
	return pass, nil
}

// DeletePassphrase removes a room's passphrase from the system keychain.
func DeletePassphrase(name string) error {
	return keyring.Delete(keychainService, "clipboard:"+name)
}

// HasPassphrase checks if a passphrase exists for a room.
func HasPassphrase(name string) bool {
	_, err := keyring.Get(keychainService, "clipboard:"+name)
	return err == nil
}

// SetAPIKey stores the Ably API key in the system keychain.
// Returns an error if the key doesn't look like a valid Ably key (key:secret format).
func SetAPIKey(key string) error {
	if !strings.Contains(key, ":") || len(key) < 20 {
		return fmt.Errorf("invalid Ably API key format (expected key:secret, at least 20 chars)")
	}
	return keyring.Set(keychainService, keychainAPIKey, key)
}

// GetAPIKey retrieves the Ably API key from the system keychain.
func GetAPIKey() (string, error) {
	key, err := keyring.Get(keychainService, keychainAPIKey)
	if err != nil {
		return "", fmt.Errorf("no API key found in keychain: %w", err)
	}
	return key, nil
}

// DeleteAPIKey removes the Ably API key from the system keychain.
func DeleteAPIKey() error {
	return keyring.Delete(keychainService, keychainAPIKey)
}
