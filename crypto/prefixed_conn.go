package crypto

import (
	"net"
	"time"
)

// PrefixedConn wraps a net.Conn with prefixed bytes that are read first
// This is useful when we've peeked bytes to determine protocol but need
// to pass the full stream to a reader that expects those bytes
type PrefixedConn struct {
	conn   net.Conn
	prefix []byte
}

// NewPrefixedConn creates a connection wrapper that returns prefix bytes
// before reading from the underlying connection
func NewPrefixedConn(conn net.Conn, prefix []byte) *PrefixedConn {
	return &PrefixedConn{
		conn:   conn,
		prefix: prefix,
	}
}

// Read implements net.Conn, returning prefix bytes first
func (pc *PrefixedConn) Read(b []byte) (int, error) {
	if len(pc.prefix) > 0 {
		n := copy(b, pc.prefix)
		pc.prefix = pc.prefix[n:]
		return n, nil
	}
	return pc.conn.Read(b)
}

// Write implements net.Conn
func (pc *PrefixedConn) Write(b []byte) (int, error) {
	return pc.conn.Write(b)
}

// Close implements net.Conn
func (pc *PrefixedConn) Close() error {
	return pc.conn.Close()
}

// LocalAddr implements net.Conn
func (pc *PrefixedConn) LocalAddr() net.Addr {
	return pc.conn.LocalAddr()
}

// RemoteAddr implements net.Conn
func (pc *PrefixedConn) RemoteAddr() net.Addr {
	return pc.conn.RemoteAddr()
}

// SetDeadline implements net.Conn
func (pc *PrefixedConn) SetDeadline(t time.Time) error {
	return pc.conn.SetDeadline(t)
}

// SetReadDeadline implements net.Conn
func (pc *PrefixedConn) SetReadDeadline(t time.Time) error {
	return pc.conn.SetReadDeadline(t)
}

// SetWriteDeadline implements net.Conn
func (pc *PrefixedConn) SetWriteDeadline(t time.Time) error {
	return pc.conn.SetWriteDeadline(t)
}
