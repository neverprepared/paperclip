package clipboard

import (
	"bytes"
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

// Simple base64 encoding/decoding to avoid importing encoding/base64
// and keep binary size minimal
const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func encodeBase64(data []byte) []byte {
	var buf bytes.Buffer
	for i := 0; i < len(data); i += 3 {
		var b uint32
		n := 0
		for j := 0; j < 3 && i+j < len(data); j++ {
			b = (b << 8) | uint32(data[i+j])
			n++
		}
		b <<= (3 - n) * 8

		chars := 4
		if n == 1 {
			chars = 2
		} else if n == 2 {
			chars = 3
		}

		for j := 0; j < chars; j++ {
			idx := (b >> (18 - j*6)) & 0x3F
			buf.WriteByte(base64Chars[idx])
		}
		for j := chars; j < 4; j++ {
			buf.WriteByte('=')
		}
	}
	return buf.Bytes()
}

func decodeBase64(dst, src []byte) (int, error) {
	var decodeMap [256]byte
	for i := range decodeMap {
		decodeMap[i] = 0xFF
	}
	for i, c := range base64Chars {
		decodeMap[c] = byte(i)
	}

	n := 0
	var buf uint32
	bits := 0

	for _, c := range src {
		if c == '=' || c == '\n' || c == '\r' {
			continue
		}
		val := decodeMap[c]
		if val == 0xFF {
			continue
		}
		buf = (buf << 6) | uint32(val)
		bits += 6
		if bits >= 8 {
			bits -= 8
			dst[n] = byte(buf >> bits)
			n++
		}
	}
	return n, nil
}
