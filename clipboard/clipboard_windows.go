//go:build windows

package clipboard

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	openClipboard       = user32.NewProc("OpenClipboard")
	closeClipboard      = user32.NewProc("CloseClipboard")
	emptyClipboard      = user32.NewProc("EmptyClipboard")
	getClipboardData    = user32.NewProc("GetClipboardData")
	setClipboardData    = user32.NewProc("SetClipboardData")
	isClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	registerClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")

	globalAlloc = kernel32.NewProc("GlobalAlloc")
	globalFree  = kernel32.NewProc("GlobalFree")
	globalLock  = kernel32.NewProc("GlobalLock")
	globalUnlock = kernel32.NewProc("GlobalUnlock")
	globalSize  = kernel32.NewProc("GlobalSize")
)

const (
	cfUnicodeText = 13
	cfDIB         = 8
	gmemMoveable  = 0x0002
)

var cfPNG uint32 // Registered at init

func init() {
	// Register PNG format - Windows supports this on modern versions
	name, _ := syscall.UTF16PtrFromString("PNG")
	ret, _, _ := registerClipboardFormatW.Call(uintptr(unsafe.Pointer(name)))
	cfPNG = uint32(ret)
}

// Read returns the current clipboard content (text or image)
func (c *Clipboard) Read() (*Content, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := openCB(); err != nil {
		return nil, err
	}
	defer closeClipboard.Call()

	// Try PNG image first
	if cfPNG != 0 {
		if data, err := getFormat(cfPNG); err == nil && len(data) > 0 {
			hash := hashData(data)
			return &Content{Type: TypeImage, Data: data, Hash: hash}, nil
		}
	}

	// Try DIB image and convert to PNG
	if data, err := getFormat(cfDIB); err == nil && len(data) > 0 {
		pngData, err := dibToPNG(data)
		if err == nil && len(pngData) > 0 {
			hash := hashData(pngData)
			return &Content{Type: TypeImage, Data: pngData, Hash: hash}, nil
		}
	}

	// Fall back to text
	data, err := getFormat(cfUnicodeText)
	if err != nil {
		return nil, err
	}

	// Convert UTF-16LE to UTF-8
	text := utf16ToUTF8(data)
	hash := hashData(text)
	return &Content{Type: TypeText, Data: text, Hash: hash}, nil
}

// Write sets the clipboard content
func (c *Clipboard) Write(content *Content) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := openCB(); err != nil {
		return err
	}
	defer closeClipboard.Call()

	emptyClipboard.Call()

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

func (c *Clipboard) writeText(data []byte) error {
	// Convert UTF-8 to UTF-16LE with null terminator
	u16 := utf16.Encode([]rune(string(data)))
	u16 = append(u16, 0) // null terminator

	size := len(u16) * 2
	hMem, _, err := globalAlloc.Call(gmemMoveable, uintptr(size))
	if hMem == 0 {
		return fmt.Errorf("GlobalAlloc failed: %v", err)
	}

	ptr, _, err := globalLock.Call(hMem)
	if ptr == 0 {
		globalFree.Call(hMem)
		return fmt.Errorf("GlobalLock failed: %v", err)
	}

	// Copy UTF-16 data
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(u16))
	copy(dst, u16)

	globalUnlock.Call(hMem)

	ret, _, err := setClipboardData.Call(cfUnicodeText, hMem)
	if ret == 0 {
		globalFree.Call(hMem)
		return fmt.Errorf("SetClipboardData failed: %v", err)
	}

	return nil
}

func (c *Clipboard) writeImage(pngData []byte) error {
	// Try to set as PNG format first
	if cfPNG != 0 {
		if err := setFormat(cfPNG, pngData); err == nil {
			return nil
		}
	}

	// Fall back to DIB format
	dibData, err := pngToDIB(pngData)
	if err != nil {
		return err
	}
	return setFormat(cfDIB, dibData)
}

func openCB() error {
	ret, _, err := openClipboard.Call(0)
	if ret == 0 {
		return fmt.Errorf("OpenClipboard failed: %v", err)
	}
	return nil
}

func getFormat(format uint32) ([]byte, error) {
	ret, _, _ := isClipboardFormatAvailable.Call(uintptr(format))
	if ret == 0 {
		return nil, errors.New("format not available")
	}

	hMem, _, err := getClipboardData.Call(uintptr(format))
	if hMem == 0 {
		return nil, fmt.Errorf("GetClipboardData failed: %v", err)
	}

	size, _, _ := globalSize.Call(hMem)
	if size == 0 {
		return nil, errors.New("empty clipboard data")
	}

	ptr, _, err := globalLock.Call(hMem)
	if ptr == 0 {
		return nil, fmt.Errorf("GlobalLock failed: %v", err)
	}
	defer globalUnlock.Call(hMem)

	data := make([]byte, size)
	copy(data, unsafe.Slice((*byte)(unsafe.Pointer(ptr)), size))
	return data, nil
}

func setFormat(format uint32, data []byte) error {
	size := len(data)
	hMem, _, err := globalAlloc.Call(gmemMoveable, uintptr(size))
	if hMem == 0 {
		return fmt.Errorf("GlobalAlloc failed: %v", err)
	}

	ptr, _, err := globalLock.Call(hMem)
	if ptr == 0 {
		globalFree.Call(hMem)
		return fmt.Errorf("GlobalLock failed: %v", err)
	}

	copy(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), size), data)
	globalUnlock.Call(hMem)

	ret, _, err := setClipboardData.Call(uintptr(format), hMem)
	if ret == 0 {
		globalFree.Call(hMem)
		return fmt.Errorf("SetClipboardData failed: %v", err)
	}

	return nil
}

func utf16ToUTF8(data []byte) []byte {
	if len(data) < 2 {
		return nil
	}

	// Convert bytes to uint16 slice (little-endian)
	u16 := make([]uint16, len(data)/2)
	for i := range u16 {
		u16[i] = binary.LittleEndian.Uint16(data[i*2:])
	}

	// Remove null terminator if present
	for len(u16) > 0 && u16[len(u16)-1] == 0 {
		u16 = u16[:len(u16)-1]
	}

	return []byte(string(utf16.Decode(u16)))
}

// DIB to PNG conversion - minimal implementation
// DIB format: BITMAPINFOHEADER followed by pixel data
func dibToPNG(dib []byte) ([]byte, error) {
	if len(dib) < 40 {
		return nil, errors.New("invalid DIB: too small")
	}

	// Parse BITMAPINFOHEADER
	width := int32(binary.LittleEndian.Uint32(dib[4:8]))
	height := int32(binary.LittleEndian.Uint32(dib[8:12]))
	bitCount := binary.LittleEndian.Uint16(dib[14:16])

	if width <= 0 || height == 0 {
		return nil, errors.New("invalid DIB dimensions")
	}

	// Handle bottom-up (positive height) vs top-down (negative height)
	bottomUp := height > 0
	if height < 0 {
		height = -height
	}

	if bitCount != 24 && bitCount != 32 {
		return nil, fmt.Errorf("unsupported bit depth: %d", bitCount)
	}

	// Calculate row stride (rows are padded to 4-byte boundaries)
	bytesPerPixel := int(bitCount) / 8
	rowSize := ((int(width)*bytesPerPixel + 3) / 4) * 4
	pixelOffset := 40 // After BITMAPINFOHEADER

	if len(dib) < pixelOffset+rowSize*int(height) {
		return nil, errors.New("invalid DIB: insufficient pixel data")
	}

	// Create PNG
	var buf bytes.Buffer

	// PNG signature
	buf.Write([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})

	// IHDR chunk
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8] = 8  // bit depth
	ihdr[9] = 2  // color type: RGB
	ihdr[10] = 0 // compression
	ihdr[11] = 0 // filter
	ihdr[12] = 0 // interlace
	writeChunk(&buf, "IHDR", ihdr)

	// IDAT chunk - uncompressed for simplicity (zlib stored block)
	var rawData bytes.Buffer
	for y := 0; y < int(height); y++ {
		srcY := y
		if bottomUp {
			srcY = int(height) - 1 - y
		}
		rowStart := pixelOffset + srcY*rowSize

		rawData.WriteByte(0) // filter byte: none
		for x := 0; x < int(width); x++ {
			pixelStart := rowStart + x*bytesPerPixel
			// DIB is BGR(A), PNG is RGB
			rawData.WriteByte(dib[pixelStart+2]) // R
			rawData.WriteByte(dib[pixelStart+1]) // G
			rawData.WriteByte(dib[pixelStart+0]) // B
		}
	}

	// Compress with zlib (deflate stored blocks for simplicity)
	compressed := zlibCompress(rawData.Bytes())
	writeChunk(&buf, "IDAT", compressed)

	// IEND chunk
	writeChunk(&buf, "IEND", nil)

	return buf.Bytes(), nil
}

// PNG to DIB conversion
func pngToDIB(png []byte) ([]byte, error) {
	if len(png) < 8 || string(png[1:4]) != "PNG" {
		return nil, errors.New("invalid PNG signature")
	}

	// Parse PNG chunks to find IHDR and IDAT
	var width, height uint32
	var bitDepth, colorType byte
	var idatData []byte

	pos := 8
	for pos+8 <= len(png) {
		chunkLen := binary.BigEndian.Uint32(png[pos:])
		chunkType := string(png[pos+4 : pos+8])
		chunkData := png[pos+8 : pos+8+int(chunkLen)]

		switch chunkType {
		case "IHDR":
			if len(chunkData) >= 13 {
				width = binary.BigEndian.Uint32(chunkData[0:4])
				height = binary.BigEndian.Uint32(chunkData[4:8])
				bitDepth = chunkData[8]
				colorType = chunkData[9]
			}
		case "IDAT":
			idatData = append(idatData, chunkData...)
		case "IEND":
			break
		}
		pos += 12 + int(chunkLen) // length + type + data + crc
	}

	if width == 0 || height == 0 {
		return nil, errors.New("invalid PNG: missing IHDR")
	}

	if bitDepth != 8 || (colorType != 2 && colorType != 6) {
		return nil, fmt.Errorf("unsupported PNG format: depth=%d type=%d", bitDepth, colorType)
	}

	// Decompress IDAT
	rawData, err := zlibDecompress(idatData)
	if err != nil {
		return nil, fmt.Errorf("zlib decompress failed: %v", err)
	}

	// Calculate sizes
	srcBytesPerPixel := 3
	if colorType == 6 {
		srcBytesPerPixel = 4 // RGBA
	}
	srcRowSize := 1 + int(width)*srcBytesPerPixel // +1 for filter byte

	dstBytesPerPixel := 3 // 24-bit BGR
	dstRowSize := ((int(width)*dstBytesPerPixel + 3) / 4) * 4

	// Create DIB
	dibSize := 40 + dstRowSize*int(height)
	dib := make([]byte, dibSize)

	// BITMAPINFOHEADER
	binary.LittleEndian.PutUint32(dib[0:4], 40)          // biSize
	binary.LittleEndian.PutUint32(dib[4:8], width)       // biWidth
	binary.LittleEndian.PutUint32(dib[8:12], height)     // biHeight (positive = bottom-up)
	binary.LittleEndian.PutUint16(dib[12:14], 1)         // biPlanes
	binary.LittleEndian.PutUint16(dib[14:16], 24)        // biBitCount
	binary.LittleEndian.PutUint32(dib[20:24], uint32(dstRowSize*int(height))) // biSizeImage

	// Convert pixels (PNG is top-down, DIB is bottom-up)
	for y := 0; y < int(height); y++ {
		srcRow := y * srcRowSize
		dstRow := 40 + (int(height)-1-y)*dstRowSize

		if srcRow >= len(rawData) {
			break
		}

		// Skip filter byte, apply no de-filtering (assumes filter=0)
		for x := 0; x < int(width); x++ {
			srcPixel := srcRow + 1 + x*srcBytesPerPixel
			dstPixel := dstRow + x*dstBytesPerPixel

			if srcPixel+2 < len(rawData) && dstPixel+2 < len(dib) {
				// RGB -> BGR
				dib[dstPixel+0] = rawData[srcPixel+2] // B
				dib[dstPixel+1] = rawData[srcPixel+1] // G
				dib[dstPixel+2] = rawData[srcPixel+0] // R
			}
		}
	}

	return dib, nil
}

func writeChunk(buf *bytes.Buffer, chunkType string, data []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	buf.Write(length[:])
	buf.WriteString(chunkType)
	buf.Write(data)

	// CRC32 of type + data
	crc := crc32(append([]byte(chunkType), data...))
	var crcBytes [4]byte
	binary.BigEndian.PutUint32(crcBytes[:], crc)
	buf.Write(crcBytes[:])
}

// Minimal CRC32 for PNG
func crc32(data []byte) uint32 {
	var table [256]uint32
	for i := 0; i < 256; i++ {
		c := uint32(i)
		for j := 0; j < 8; j++ {
			if c&1 != 0 {
				c = 0xEDB88320 ^ (c >> 1)
			} else {
				c >>= 1
			}
		}
		table[i] = c
	}

	crc := uint32(0xFFFFFFFF)
	for _, b := range data {
		crc = table[(crc^uint32(b))&0xFF] ^ (crc >> 8)
	}
	return crc ^ 0xFFFFFFFF
}

// Minimal zlib compression using stored blocks (no actual compression)
func zlibCompress(data []byte) []byte {
	var buf bytes.Buffer

	// Zlib header (no compression)
	buf.WriteByte(0x78) // CMF: deflate, 32K window
	buf.WriteByte(0x01) // FLG: no dict, fastest

	// Split into stored blocks (max 65535 bytes each)
	for len(data) > 0 {
		blockSize := len(data)
		if blockSize > 65535 {
			blockSize = 65535
		}
		final := byte(0)
		if blockSize == len(data) {
			final = 1
		}

		buf.WriteByte(final)                                       // BFINAL + BTYPE=00 (stored)
		buf.WriteByte(byte(blockSize))                             // LEN low
		buf.WriteByte(byte(blockSize >> 8))                        // LEN high
		buf.WriteByte(byte(^blockSize))                            // NLEN low
		buf.WriteByte(byte((^blockSize) >> 8))                     // NLEN high
		buf.Write(data[:blockSize])

		data = data[blockSize:]
	}

	// Adler32 checksum
	a := adler32(buf.Bytes()[2:]) // Skip zlib header
	buf.WriteByte(byte(a >> 24))
	buf.WriteByte(byte(a >> 16))
	buf.WriteByte(byte(a >> 8))
	buf.WriteByte(byte(a))

	return buf.Bytes()
}

// Minimal zlib decompression (handles stored blocks)
func zlibDecompress(data []byte) ([]byte, error) {
	if len(data) < 6 {
		return nil, errors.New("zlib data too short")
	}

	// Skip zlib header (2 bytes) and checksum (4 bytes at end)
	deflate := data[2 : len(data)-4]

	var result []byte
	pos := 0

	for pos < len(deflate) {
		if pos >= len(deflate) {
			break
		}

		header := deflate[pos]
		btype := (header >> 1) & 3
		pos++

		if btype == 0 {
			// Stored block
			if pos+4 > len(deflate) {
				return nil, errors.New("invalid stored block")
			}
			length := uint16(deflate[pos]) | (uint16(deflate[pos+1]) << 8)
			pos += 4 // Skip LEN and NLEN

			if pos+int(length) > len(deflate) {
				return nil, errors.New("stored block exceeds data")
			}
			result = append(result, deflate[pos:pos+int(length)]...)
			pos += int(length)
		} else {
			// Compressed blocks not supported in this minimal impl
			return nil, fmt.Errorf("compressed deflate blocks not supported (type=%d)", btype)
		}

		if header&1 != 0 {
			break // BFINAL
		}
	}

	return result, nil
}

func adler32(data []byte) uint32 {
	a, b := uint32(1), uint32(0)
	for _, c := range data {
		a = (a + uint32(c)) % 65521
		b = (b + a) % 65521
	}
	return (b << 16) | a
}
