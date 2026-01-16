package clipboard

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os/exec"
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

// Clipboard handles macOS clipboard operations
type Clipboard struct {
	mu       sync.Mutex
	lastHash string
	logger   *log.Logger
}

// New creates a new Clipboard instance
func New(logger *log.Logger) *Clipboard {
	return &Clipboard{logger: logger}
}

// Read returns the current clipboard content (text or image)
func (c *Clipboard) Read() (*Content, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Try to read image first (PNG from clipboard)
	imgData, imgErr := c.readImage()
	if imgErr == nil && len(imgData) > 0 {
		hash := hashData(imgData)
		return &Content{
			Type: TypeImage,
			Data: imgData,
			Hash: hash,
		}, nil
	}

	// Fall back to text
	textData, textErr := c.readText()
	if textErr != nil {
		return nil, textErr
	}

	hash := hashData(textData)
	return &Content{
		Type: TypeText,
		Data: textData,
		Hash: hash,
	}, nil
}

// Write sets the clipboard content
func (c *Clipboard) Write(content *Content) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var err error
	switch content.Type {
	case TypeImage:
		err = c.writeImage(content.Data)
	default:
		err = c.writeText(content.Data)
	}

	if err == nil {
		c.lastHash = content.Hash
	}
	return err
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

func (c *Clipboard) readText() ([]byte, error) {
	cmd := exec.Command("pbpaste")
	return cmd.Output()
}

func (c *Clipboard) writeText(data []byte) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = bytes.NewReader(data)
	return cmd.Run()
}

func (c *Clipboard) readImage() ([]byte, error) {
	// Use osascript to get clipboard as PNG data (convert from TIFF if needed)
	// macOS clipboard often stores images as TIFF, so we convert to PNG for portability
	script := `use framework "AppKit"
use framework "Foundation"
use scripting additions

set theClipboard to current application's NSPasteboard's generalPasteboard()

-- Try PNG first
set imgData to theClipboard's dataForType:(current application's NSPasteboardTypePNG)

-- Fall back to TIFF and convert to PNG
if imgData is missing value then
    set tiffData to theClipboard's dataForType:(current application's NSPasteboardTypeTIFF)
    if tiffData is missing value then
        error "No image"
    end if

    -- Convert TIFF to PNG via NSBitmapImageRep
    set imgRep to current application's NSBitmapImageRep's imageRepWithData:tiffData
    if imgRep is missing value then
        error "No image"
    end if
    set imgData to imgRep's representationUsingType:(current application's NSBitmapImageFileTypePNG) |properties|:(missing value)
end if

return (imgData's base64EncodedStringWithOptions:0) as text`

	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	// Decode base64
	output = bytes.TrimSpace(output)
	decoded := make([]byte, len(output))
	n, err := decodeBase64(decoded, output)
	if err != nil {
		return nil, err
	}
	return decoded[:n], nil
}

func (c *Clipboard) writeImage(data []byte) error {
	// Use osascript to write PNG to clipboard
	// Note: Must use class "NSData" syntax for proper class resolution
	encoded := encodeBase64(data)
	script := fmt.Sprintf(`use framework "AppKit"
use framework "Foundation"
use scripting additions

set b64Data to "%s"
set nsData to current application's class "NSData"'s alloc()'s initWithBase64EncodedString:b64Data options:0
set theClipboard to current application's NSPasteboard's generalPasteboard()
theClipboard's clearContents()
theClipboard's setData:nsData forType:(current application's NSPasteboardTypePNG)
`, string(encoded))

	cmd := exec.Command("osascript", "-e", script)
	return cmd.Run()
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
