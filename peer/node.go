package peer

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/flynn/noise"
	"github.com/mindmorass/paperclip/clipboard"
	"github.com/mindmorass/paperclip/crypto"
)

const (
	maxMessageSize = 10 * 1024 * 1024 // 10MB max for images
	headerSize     = 5                // 1 byte type + 4 bytes length
	maxBackoff     = 30 * time.Second
	initialBackoff = 1 * time.Second
	dialTimeout    = 3 * time.Second
)

// Node represents a peer in the clipboard sharing network
type Node struct {
	port       int
	peerGroups []*peerGroup
	clipboard  *clipboard.Clipboard
	logger     *log.Logger
	verbose    bool

	// Crypto (optional - nil if no noise: peers)
	identity   noise.DHKey
	knownHosts *crypto.KnownHosts
	usesCrypto bool

	listener net.Listener

	// Track inbound connections separately
	inbound   map[string]net.Conn
	inboundMu sync.RWMutex

	seenHashes   map[string]time.Time
	seenHashesMu sync.Mutex

	stopChan chan struct{}
	wg       sync.WaitGroup
}

// peerGroup represents a single logical peer reachable via multiple addresses
// Use pipe (|) to separate addresses: "192.168.1.100:9999|100.64.0.5:9999"
// Use "noise:" prefix for encrypted connections: "noise:public.example.com:9999"
type peerGroup struct {
	name       string          // friendly name
	addrs      []string        // all addresses for this peer (without noise: prefix)
	noiseAddrs map[string]bool // which addresses use noise encryption
	mu         sync.Mutex
	conn       net.Conn
	connected  bool
	activeAddr string
	encrypted  bool // whether current connection is encrypted
	backoff    time.Duration
	lastTry    time.Time
}

// NewNode creates a new peer node
// Peers format: "addr1:port|addr2:port,other-peer:port"
// Use | to group multiple addresses for the same peer (e.g., LAN + Tailscale)
// Use , to separate different peers
// Use "noise:" prefix for encrypted connections: "noise:host:port"
func NewNode(port int, peers string, cb *clipboard.Clipboard, logger *log.Logger, verbose bool, identity noise.DHKey, knownHosts *crypto.KnownHosts) *Node {
	var groups []*peerGroup
	usesCrypto := false

	if peers != "" {
		// Split by comma for different peers
		for _, peerSpec := range strings.Split(peers, ",") {
			peerSpec = strings.TrimSpace(peerSpec)
			if peerSpec == "" {
				continue
			}

			// Split by pipe for same peer, multiple addresses
			var addrs []string
			noiseAddrs := make(map[string]bool)

			for _, addr := range strings.Split(peerSpec, "|") {
				addr = strings.TrimSpace(addr)
				if addr == "" {
					continue
				}

				// Check for noise: prefix
				if strings.HasPrefix(addr, "noise:") {
					actualAddr := strings.TrimPrefix(addr, "noise:")
					addrs = append(addrs, actualAddr)
					noiseAddrs[actualAddr] = true
					usesCrypto = true
				} else {
					addrs = append(addrs, addr)
				}
			}

			if len(addrs) > 0 {
				// Use the first address (with noise: prefix if applicable) as name
				name := addrs[0]
				if noiseAddrs[addrs[0]] {
					name = "noise:" + addrs[0]
				}

				groups = append(groups, &peerGroup{
					name:       name,
					addrs:      addrs,
					noiseAddrs: noiseAddrs,
					backoff:    initialBackoff,
				})
			}
		}
	}

	return &Node{
		port:       port,
		peerGroups: groups,
		clipboard:  cb,
		logger:     logger,
		verbose:    verbose,
		identity:   identity,
		knownHosts: knownHosts,
		usesCrypto: usesCrypto,
		inbound:    make(map[string]net.Conn),
		seenHashes: make(map[string]time.Time),
		stopChan:   make(chan struct{}),
	}
}

// Start begins listening and connects to peers
func (n *Node) Start(pollMs int) error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", n.port))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	n.listener = listener

	// Accept incoming connections
	n.wg.Add(1)
	go n.acceptLoop()

	// Maintain connections to each peer group
	for _, pg := range n.peerGroups {
		n.wg.Add(1)
		go n.maintainPeerGroup(pg)
	}

	// Poll clipboard
	n.wg.Add(1)
	go n.pollClipboard(time.Duration(pollMs) * time.Millisecond)

	// Cleanup old hashes
	n.wg.Add(1)
	go n.cleanupSeenHashes()

	n.wg.Wait()
	return nil
}

// Stop gracefully shuts down the node
func (n *Node) Stop() {
	close(n.stopChan)
	if n.listener != nil {
		n.listener.Close()
	}
	for _, pg := range n.peerGroups {
		pg.mu.Lock()
		if pg.conn != nil {
			pg.conn.Close()
		}
		pg.mu.Unlock()
	}
	n.inboundMu.Lock()
	for _, conn := range n.inbound {
		conn.Close()
	}
	n.inboundMu.Unlock()
}

func (n *Node) acceptLoop() {
	defer n.wg.Done()

	for {
		select {
		case <-n.stopChan:
			return
		default:
		}

		rawConn, err := n.listener.Accept()
		if err != nil {
			select {
			case <-n.stopChan:
				return
			default:
				continue
			}
		}

		addr := rawConn.RemoteAddr().String()

		// Detect protocol by peeking first byte
		magic := make([]byte, 1)
		rawConn.SetReadDeadline(time.Now().Add(10 * time.Second))
		_, err = io.ReadFull(rawConn, magic)
		rawConn.SetReadDeadline(time.Time{})
		if err != nil {
			rawConn.Close()
			continue
		}

		var conn net.Conn
		encrypted := false

		if magic[0] == crypto.MagicNoise {
			// Noise handshake
			if !n.usesCrypto {
				if n.verbose {
					n.logger.Printf("Rejecting Noise connection from %s (crypto not enabled)", addr)
				}
				rawConn.Close()
				continue
			}

			noiseConn, peerKey, err := crypto.HandshakeResponder(rawConn, n.identity)
			if err != nil {
				if n.verbose {
					n.logger.Printf("Noise handshake failed from %s: %v", addr, err)
				}
				rawConn.Close()
				continue
			}

			// Verify peer key via TOFU
			if n.knownHosts != nil {
				if err := n.knownHosts.Verify(addr, peerKey); err != nil {
					n.logger.Printf("Key verification failed for %s: %v", addr, err)
					noiseConn.Close()
					continue
				}
			}

			conn = noiseConn
			encrypted = true
			if n.verbose {
				n.logger.Printf("Incoming encrypted connection from %s (key: %s)",
					addr, crypto.PublicKeyFingerprint(peerKey))
			}
		} else {
			// Plain connection - push the byte back
			conn = crypto.NewPrefixedConn(rawConn, magic)
			if n.verbose {
				n.logger.Printf("Incoming connection from %s", addr)
			}
		}

		n.inboundMu.Lock()
		n.inbound[addr] = conn
		n.inboundMu.Unlock()

		n.wg.Add(1)
		go n.handleInbound(conn, addr, encrypted)
	}
}

func (n *Node) handleInbound(conn net.Conn, addr string, encrypted bool) {
	defer n.wg.Done()
	defer func() {
		conn.Close()
		n.inboundMu.Lock()
		delete(n.inbound, addr)
		n.inboundMu.Unlock()
	}()

	name := addr
	if encrypted {
		name = addr + " (encrypted)"
	}
	n.readLoop(conn, name)
}

func (n *Node) maintainPeerGroup(pg *peerGroup) {
	defer n.wg.Done()

	for {
		select {
		case <-n.stopChan:
			return
		default:
		}

		pg.mu.Lock()
		if pg.connected {
			pg.mu.Unlock()
			time.Sleep(time.Second)
			continue
		}

		// Check backoff
		if time.Since(pg.lastTry) < pg.backoff {
			pg.mu.Unlock()
			time.Sleep(500 * time.Millisecond)
			continue
		}
		pg.lastTry = time.Now()
		addrs := pg.addrs
		noiseAddrs := pg.noiseAddrs
		pg.mu.Unlock()

		// Try all addresses in parallel, use first to connect
		conn, addr, encrypted := n.dialAny(addrs, noiseAddrs)
		if conn == nil {
			pg.mu.Lock()
			pg.backoff = min(pg.backoff*2, maxBackoff)
			pg.mu.Unlock()
			if n.verbose {
				n.logger.Printf("Failed to connect to %s (tried %d addrs, backoff: %v)",
					pg.name, len(addrs), pg.backoff)
			}
			continue
		}

		pg.mu.Lock()
		pg.conn = conn
		pg.connected = true
		pg.activeAddr = addr
		pg.encrypted = encrypted
		pg.backoff = initialBackoff
		pg.mu.Unlock()

		if encrypted {
			n.logger.Printf("Connected to %s via %s (encrypted)", pg.name, addr)
		} else {
			n.logger.Printf("Connected to %s via %s", pg.name, addr)
		}

		// Handle this connection
		n.wg.Add(1)
		go n.handleOutbound(pg, conn)
	}
}

// dialAny tries connecting to all addresses in parallel, returns first success
// Returns: connection (possibly wrapped with Noise), address, and whether encrypted
func (n *Node) dialAny(addrs []string, noiseAddrs map[string]bool) (net.Conn, string, bool) {
	if len(addrs) == 0 {
		return nil, "", false
	}

	type result struct {
		conn      net.Conn
		addr      string
		encrypted bool
		err       error
	}

	ctx := make(chan struct{})
	results := make(chan result, len(addrs))

	for _, addr := range addrs {
		go func(a string) {
			useNoise := noiseAddrs[a]

			rawConn, err := net.DialTimeout("tcp", a, dialTimeout)
			select {
			case <-ctx:
				// Another address won, close this connection
				if rawConn != nil {
					rawConn.Close()
				}
				return
			default:
			}

			if err != nil {
				results <- result{nil, a, false, err}
				return
			}

			// If this address uses Noise, do the handshake
			if useNoise {
				noiseConn, peerKey, err := crypto.HandshakeInitiator(rawConn, n.identity)
				if err != nil {
					rawConn.Close()
					results <- result{nil, a, false, fmt.Errorf("noise handshake failed: %w", err)}
					return
				}

				// Verify peer key via TOFU
				if n.knownHosts != nil {
					if err := n.knownHosts.Verify(a, peerKey); err != nil {
						noiseConn.Close()
						results <- result{nil, a, false, err}
						return
					}
				}

				results <- result{noiseConn, a, true, nil}
			} else {
				results <- result{rawConn, a, false, nil}
			}
		}(addr)
	}

	// Wait for first success or all failures
	var winner result
	var lastErr error
	for i := 0; i < len(addrs); i++ {
		r := <-results
		if r.err != nil {
			lastErr = r.err
			continue
		}
		if r.conn != nil && winner.conn == nil {
			winner = r
			close(ctx) // Signal others to stop
		} else if r.conn != nil {
			r.conn.Close() // Close extra connections
		}
	}

	if winner.conn == nil && lastErr != nil && n.verbose {
		n.logger.Printf("Dial error: %v", lastErr)
	}

	return winner.conn, winner.addr, winner.encrypted
}

func (n *Node) handleOutbound(pg *peerGroup, conn net.Conn) {
	defer n.wg.Done()
	defer func() {
		conn.Close()
		pg.mu.Lock()
		pg.connected = false
		pg.conn = nil
		pg.activeAddr = ""
		pg.mu.Unlock()
		if n.verbose {
			n.logger.Printf("Disconnected from %s", pg.name)
		}
	}()

	n.readLoop(conn, pg.name)
}

func (n *Node) readLoop(conn net.Conn, name string) {
	for {
		select {
		case <-n.stopChan:
			return
		default:
		}

		conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		header := make([]byte, headerSize)
		_, err := io.ReadFull(conn, header)
		if err != nil {
			if err != io.EOF && n.verbose {
				n.logger.Printf("Read error from %s: %v", name, err)
			}
			return
		}

		contentType := clipboard.ContentType(header[0])
		length := binary.BigEndian.Uint32(header[1:])

		if length > maxMessageSize {
			n.logger.Printf("Message too large from %s: %d bytes", name, length)
			return
		}

		data := make([]byte, length)
		_, err = io.ReadFull(conn, data)
		if err != nil {
			n.logger.Printf("Failed to read data from %s: %v", name, err)
			return
		}

		content := &clipboard.Content{
			Type: contentType,
			Data: data,
			Hash: hashContent(data),
		}

		// Echo prevention
		if n.isSeenRecently(content.Hash) {
			if n.verbose {
				n.logger.Printf("Skipping duplicate (hash: %s...)", content.Hash[:8])
			}
			continue
		}

		n.markSeen(content.Hash)

		if err := n.clipboard.Write(content); err != nil {
			n.logger.Printf("Failed to write to clipboard: %v", err)
			continue
		}

		if n.verbose {
			typeStr := "text"
			if content.Type == clipboard.TypeImage {
				typeStr = "image"
			}
			n.logger.Printf("Received %s (%d bytes) from %s", typeStr, length, name)
		}
	}
}

func (n *Node) pollClipboard(interval time.Duration) {
	defer n.wg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-n.stopChan:
			return
		case <-ticker.C:
			content, err := n.clipboard.Read()
			if err != nil {
				continue
			}

			if !n.clipboard.HasChanged(content.Hash) {
				continue
			}

			if n.isSeenRecently(content.Hash) {
				n.clipboard.SetLastHash(content.Hash)
				continue
			}

			n.markSeen(content.Hash)
			n.clipboard.SetLastHash(content.Hash)

			if n.verbose {
				typeStr := "text"
				if content.Type == clipboard.TypeImage {
					typeStr = "image"
				}
				n.logger.Printf("Clipboard changed: %s (%d bytes)", typeStr, len(content.Data))
			}

			n.broadcast(content)
		}
	}
}

func (n *Node) broadcast(content *clipboard.Content) {
	msg := make([]byte, headerSize+len(content.Data))
	msg[0] = byte(content.Type)
	binary.BigEndian.PutUint32(msg[1:], uint32(len(content.Data)))
	copy(msg[headerSize:], content.Data)

	// Send to all peer groups
	for _, pg := range n.peerGroups {
		pg.mu.Lock()
		if pg.connected && pg.conn != nil {
			pg.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_, err := pg.conn.Write(msg)
			if err != nil {
				if n.verbose {
					n.logger.Printf("Failed to send to %s: %v", pg.name, err)
				}
				pg.conn.Close()
				pg.connected = false
				pg.conn = nil
			} else if n.verbose {
				n.logger.Printf("Sent to %s", pg.name)
			}
		}
		pg.mu.Unlock()
	}

	// Also send to inbound connections
	n.inboundMu.RLock()
	for addr, conn := range n.inbound {
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err := conn.Write(msg)
		if err != nil && n.verbose {
			n.logger.Printf("Failed to send to inbound %s: %v", addr, err)
		} else if n.verbose {
			n.logger.Printf("Sent to inbound %s", addr)
		}
	}
	n.inboundMu.RUnlock()
}

func (n *Node) isSeenRecently(hash string) bool {
	n.seenHashesMu.Lock()
	defer n.seenHashesMu.Unlock()
	if t, ok := n.seenHashes[hash]; ok {
		return time.Since(t) < 5*time.Second
	}
	return false
}

func (n *Node) markSeen(hash string) {
	n.seenHashesMu.Lock()
	defer n.seenHashesMu.Unlock()
	n.seenHashes[hash] = time.Now()
}

func (n *Node) cleanupSeenHashes() {
	defer n.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-n.stopChan:
			return
		case <-ticker.C:
			n.seenHashesMu.Lock()
			now := time.Now()
			for hash, t := range n.seenHashes {
				if now.Sub(t) > 30*time.Second {
					delete(n.seenHashes, hash)
				}
			}
			n.seenHashesMu.Unlock()
		}
	}
}

func hashContent(data []byte) string {
	var h uint64
	for _, b := range data {
		h = h*31 + uint64(b)
	}
	return fmt.Sprintf("%016x", h)
}
