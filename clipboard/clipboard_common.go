package clipboard

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"sync"
)

// ContentType identifies the type of clipboard content
type ContentType byte

const (
	TypeText  ContentType = 0x01
	TypeImage ContentType = 0x02
)

// Content represents clipboard data with its type and hash
type Content struct {
	Type ContentType
	Data []byte
	Hash string
}

// Clipboard handles clipboard operations
type Clipboard struct {
	mu       sync.Mutex
	lastHash string
	logger   *log.Logger
}

// New creates a new Clipboard instance
func New(logger *log.Logger) *Clipboard {
	return &Clipboard{logger: logger}
}

// HasChanged returns true if clipboard content differs from last known hash
func (c *Clipboard) HasChanged(currentHash string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return currentHash != c.lastHash
}

// SetLastHash updates the last known hash (used after sending)
func (c *Clipboard) SetLastHash(hash string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastHash = hash
}

// GetLastHash returns the last known hash
func (c *Clipboard) GetLastHash() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastHash
}

func hashData(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
