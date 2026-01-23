package crypto

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/flynn/noise"
)

const (
	// MagicNoise is the first byte sent to indicate Noise handshake
	MagicNoise byte = 0x00

	// MaxMessageSize is the maximum size of a single encrypted message
	MaxMessageSize = 65535

	// NoiseProtocol is the Noise protocol name
	NoiseProtocol = "Noise_XX_25519_ChaChaPoly_BLAKE2s"
)

// NoiseConn wraps a net.Conn with Noise protocol encryption
type NoiseConn struct {
	conn      net.Conn
	cipher    *noise.CipherState // for sending
	decipher  *noise.CipherState // for receiving
	peerKey   []byte             // peer's static public key
	readBuf   []byte             // buffered decrypted data
	writeMu   sync.Mutex
	readMu    sync.Mutex
}

// HandshakeInitiator performs the client-side XX handshake
// Returns the encrypted connection and the peer's public key
func HandshakeInitiator(conn net.Conn, localKey noise.DHKey) (*NoiseConn, []byte, error) {
	// Write magic byte first to signal Noise protocol
	if _, err := conn.Write([]byte{MagicNoise}); err != nil {
		return nil, nil, fmt.Errorf("failed to write magic byte: %w", err)
	}

	cs := noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashBLAKE2s)
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   cs,
		Pattern:       noise.HandshakeXX,
		Initiator:     true,
		StaticKeypair: localKey,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create handshake state: %w", err)
	}

	// XX pattern:
	// -> e
	// <- e, ee, s, es
	// -> s, se

	// Message 1: -> e
	msg1, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to write message 1: %w", err)
	}
	if err := writeFrame(conn, msg1); err != nil {
		return nil, nil, fmt.Errorf("failed to send message 1: %w", err)
	}

	// Message 2: <- e, ee, s, es
	msg2, err := readFrame(conn)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read message 2: %w", err)
	}
	_, _, _, err = hs.ReadMessage(nil, msg2)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to process message 2: %w", err)
	}

	// Message 3: -> s, se
	msg3, cipher, decipher, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to write message 3: %w", err)
	}
	if err := writeFrame(conn, msg3); err != nil {
		return nil, nil, fmt.Errorf("failed to send message 3: %w", err)
	}

	peerKey := hs.PeerStatic()

	return &NoiseConn{
		conn:     conn,
		cipher:   cipher,
		decipher: decipher,
		peerKey:  peerKey,
	}, peerKey, nil
}

// HandshakeResponder performs the server-side XX handshake
// Returns the encrypted connection and the peer's public key
func HandshakeResponder(conn net.Conn, localKey noise.DHKey) (*NoiseConn, []byte, error) {
	cs := noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashBLAKE2s)
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   cs,
		Pattern:       noise.HandshakeXX,
		Initiator:     false,
		StaticKeypair: localKey,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create handshake state: %w", err)
	}

	// XX pattern:
	// -> e
	// <- e, ee, s, es
	// -> s, se

	// Message 1: -> e (receive)
	msg1, err := readFrame(conn)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read message 1: %w", err)
	}
	_, _, _, err = hs.ReadMessage(nil, msg1)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to process message 1: %w", err)
	}

	// Message 2: <- e, ee, s, es (send)
	msg2, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to write message 2: %w", err)
	}
	if err := writeFrame(conn, msg2); err != nil {
		return nil, nil, fmt.Errorf("failed to send message 2: %w", err)
	}

	// Message 3: -> s, se (receive)
	msg3, err := readFrame(conn)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read message 3: %w", err)
	}
	_, decipher, cipher, err := hs.ReadMessage(nil, msg3)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to process message 3: %w", err)
	}

	peerKey := hs.PeerStatic()

	return &NoiseConn{
		conn:     conn,
		cipher:   cipher,
		decipher: decipher,
		peerKey:  peerKey,
	}, peerKey, nil
}

// PeerPublicKey returns the peer's static public key
func (nc *NoiseConn) PeerPublicKey() []byte {
	return nc.peerKey
}

// Read reads decrypted data from the connection
func (nc *NoiseConn) Read(b []byte) (int, error) {
	nc.readMu.Lock()
	defer nc.readMu.Unlock()

	// Return buffered data first
	if len(nc.readBuf) > 0 {
		n := copy(b, nc.readBuf)
		nc.readBuf = nc.readBuf[n:]
		return n, nil
	}

	// Read and decrypt a frame
	ciphertext, err := readFrame(nc.conn)
	if err != nil {
		return 0, err
	}

	plaintext, err := nc.decipher.Decrypt(nil, nil, ciphertext)
	if err != nil {
		return 0, fmt.Errorf("decryption failed: %w", err)
	}

	n := copy(b, plaintext)
	if n < len(plaintext) {
		nc.readBuf = plaintext[n:]
	}

	return n, nil
}

// Write encrypts and writes data to the connection
func (nc *NoiseConn) Write(b []byte) (int, error) {
	nc.writeMu.Lock()
	defer nc.writeMu.Unlock()

	// Fragment large messages
	total := 0
	for len(b) > 0 {
		// Leave room for auth tag (16 bytes for ChaCha20-Poly1305)
		maxPlain := MaxMessageSize - 16
		chunk := b
		if len(chunk) > maxPlain {
			chunk = b[:maxPlain]
		}
		b = b[len(chunk):]

		ciphertext, err := nc.cipher.Encrypt(nil, nil, chunk)
		if err != nil {
			return total, fmt.Errorf("encryption failed: %w", err)
		}

		if err := writeFrame(nc.conn, ciphertext); err != nil {
			return total, err
		}

		total += len(chunk)
	}

	return total, nil
}

// Close closes the underlying connection
func (nc *NoiseConn) Close() error {
	return nc.conn.Close()
}

// LocalAddr returns the local network address
func (nc *NoiseConn) LocalAddr() net.Addr {
	return nc.conn.LocalAddr()
}

// RemoteAddr returns the remote network address
func (nc *NoiseConn) RemoteAddr() net.Addr {
	return nc.conn.RemoteAddr()
}

// SetDeadline sets the read and write deadlines
func (nc *NoiseConn) SetDeadline(t time.Time) error {
	return nc.conn.SetDeadline(t)
}

// SetReadDeadline sets the deadline for Read calls
func (nc *NoiseConn) SetReadDeadline(t time.Time) error {
	return nc.conn.SetReadDeadline(t)
}

// SetWriteDeadline sets the deadline for Write calls
func (nc *NoiseConn) SetWriteDeadline(t time.Time) error {
	return nc.conn.SetWriteDeadline(t)
}

// writeFrame writes a length-prefixed frame
func writeFrame(conn net.Conn, data []byte) error {
	if len(data) > MaxMessageSize {
		return fmt.Errorf("message too large: %d > %d", len(data), MaxMessageSize)
	}

	header := make([]byte, 2)
	binary.BigEndian.PutUint16(header, uint16(len(data)))

	if _, err := conn.Write(header); err != nil {
		return err
	}
	if _, err := conn.Write(data); err != nil {
		return err
	}
	return nil
}

// readFrame reads a length-prefixed frame
func readFrame(conn net.Conn) ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint16(header)
	if length > MaxMessageSize {
		return nil, fmt.Errorf("frame too large: %d", length)
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}

	return data, nil
}
