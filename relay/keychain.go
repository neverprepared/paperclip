package relay

import (
	"fmt"

	"github.com/zalando/go-keyring"
)

const (
	keychainService = "com.github.mindmorass.paperclip"
	keychainAPIKey  = "ably-api-key"
)

// SetPassphrase stores a room's passphrase in the system keychain.
func SetPassphrase(room, passphrase string) error {
	return keyring.Set(keychainService, "room:"+room, passphrase)
}

// GetPassphrase retrieves a room's passphrase from the system keychain.
func GetPassphrase(room string) (string, error) {
	pass, err := keyring.Get(keychainService, "room:"+room)
	if err != nil {
		return "", fmt.Errorf("no passphrase found for room '%s': %w", room, err)
	}
	return pass, nil
}

// DeletePassphrase removes a room's passphrase from the system keychain.
func DeletePassphrase(room string) error {
	return keyring.Delete(keychainService, "room:"+room)
}

// HasPassphrase checks if a passphrase exists for a room.
func HasPassphrase(room string) bool {
	_, err := keyring.Get(keychainService, "room:"+room)
	return err == nil
}

// SetAPIKey stores the Ably API key in the system keychain.
func SetAPIKey(key string) error {
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
