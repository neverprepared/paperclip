//go:build darwin

package clipboard

import (
	"bytes"
	"fmt"
	"os/exec"
)

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
