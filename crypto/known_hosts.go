package crypto

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// KnownHost represents a trusted peer's public key
type KnownHost struct {
	Addresses []string  // Can be multiple (pipe-separated in config)
	PublicKey []byte    // 32-byte Curve25519 public key
	FirstSeen time.Time // When we first saw this peer
	Comment   string    // Optional user comment
}

// KnownHosts manages trusted peer public keys (TOFU model)
type KnownHosts struct {
	hosts map[string]*KnownHost // keyed by normalized address
	path  string
	mu    sync.RWMutex
}

// LoadKnownHosts loads the known_hosts file, creating an empty one if needed
func LoadKnownHosts(configDir string) (*KnownHosts, error) {
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return nil, fmt.Errorf("cannot create config directory: %w", err)
	}

	kh := &KnownHosts{
		hosts: make(map[string]*KnownHost),
		path:  filepath.Join(configDir, "known_hosts"),
	}

	data, err := os.ReadFile(kh.path)
	if os.IsNotExist(err) {
		return kh, nil // Empty known_hosts is fine
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read known_hosts: %w", err)
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		host, err := parseLine(line)
		if err != nil {
			// Log warning but continue
			continue
		}

		// Map all addresses to this host
		for _, addr := range host.Addresses {
			kh.hosts[normalizeAddr(addr)] = host
		}
	}

	return kh, nil
}

// parseLine parses a single line from known_hosts
// Format: addresses base64-pubkey [timestamp] [comment]
func parseLine(line string) (*KnownHost, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil, fmt.Errorf("invalid line: need at least address and key")
	}

	// Parse addresses (pipe-separated)
	addrs := strings.Split(fields[0], "|")

	// Parse public key
	pubKey, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}
	if len(pubKey) != 32 {
		return nil, fmt.Errorf("invalid public key length: %d", len(pubKey))
	}

	host := &KnownHost{
		Addresses: addrs,
		PublicKey: pubKey,
		FirstSeen: time.Now(),
	}

	// Optional timestamp
	if len(fields) >= 3 {
		if t, err := time.Parse(time.RFC3339, fields[2]); err == nil {
			host.FirstSeen = t
		} else {
			// Might be a comment without timestamp
			host.Comment = strings.Join(fields[2:], " ")
		}
	}

	// Optional comment (after timestamp)
	if len(fields) >= 4 {
		host.Comment = strings.Join(fields[3:], " ")
	}

	return host, nil
}

// normalizeAddr normalizes an address for lookup
func normalizeAddr(addr string) string {
	return strings.ToLower(strings.TrimSpace(addr))
}

// Lookup returns the known host for an address, if any
func (kh *KnownHosts) Lookup(addr string) (*KnownHost, bool) {
	kh.mu.RLock()
	defer kh.mu.RUnlock()

	host, ok := kh.hosts[normalizeAddr(addr)]
	return host, ok
}

// Add adds a new peer's public key (for first contact)
func (kh *KnownHosts) Add(addrs []string, pubKey []byte) error {
	kh.mu.Lock()
	defer kh.mu.Unlock()

	host := &KnownHost{
		Addresses: addrs,
		PublicKey: make([]byte, len(pubKey)),
		FirstSeen: time.Now(),
	}
	copy(host.PublicKey, pubKey)

	// Map all addresses to this host
	for _, addr := range addrs {
		kh.hosts[normalizeAddr(addr)] = host
	}

	return kh.saveLocked()
}

// Verify checks if a peer's key matches what we have stored
// Returns nil if OK, error if mismatch or other problem
func (kh *KnownHosts) Verify(addr string, pubKey []byte) error {
	kh.mu.RLock()
	existing, ok := kh.hosts[normalizeAddr(addr)]
	kh.mu.RUnlock()

	if !ok {
		// First contact - add and trust
		return kh.Add([]string{addr}, pubKey)
	}

	if bytes.Equal(existing.PublicKey, pubKey) {
		// Known and matches
		return nil
	}

	// KEY MISMATCH
	return &KeyMismatchError{
		Address:     addr,
		ExpectedKey: existing.PublicKey,
		ActualKey:   pubKey,
		FilePath:    kh.path,
	}
}

// Save writes the known_hosts file
func (kh *KnownHosts) Save() error {
	kh.mu.Lock()
	defer kh.mu.Unlock()
	return kh.saveLocked()
}

func (kh *KnownHosts) saveLocked() error {
	// Deduplicate hosts (multiple addrs may point to same host)
	seen := make(map[*KnownHost]bool)
	var hosts []*KnownHost

	for _, host := range kh.hosts {
		if !seen[host] {
			seen[host] = true
			hosts = append(hosts, host)
		}
	}

	var buf bytes.Buffer
	buf.WriteString("# Paperclip known hosts\n")
	buf.WriteString("# Format: address(es) public-key-base64 timestamp [comment]\n\n")

	for _, host := range hosts {
		addrs := strings.Join(host.Addresses, "|")
		key := base64.StdEncoding.EncodeToString(host.PublicKey)
		ts := host.FirstSeen.Format(time.RFC3339)

		if host.Comment != "" {
			fmt.Fprintf(&buf, "%s %s %s %s\n", addrs, key, ts, host.Comment)
		} else {
			fmt.Fprintf(&buf, "%s %s %s\n", addrs, key, ts)
		}
	}

	return os.WriteFile(kh.path, buf.Bytes(), 0600)
}

// KeyMismatchError is returned when a peer's key doesn't match known_hosts
type KeyMismatchError struct {
	Address     string
	ExpectedKey []byte
	ActualKey   []byte
	FilePath    string
}

func (e *KeyMismatchError) Error() string {
	return fmt.Sprintf(`
@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!    @
@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
IT IS POSSIBLE THAT SOMEONE IS DOING SOMETHING NASTY!
Someone could be eavesdropping on you right now (man-in-the-middle attack)!
It is also possible that a host key has just been changed.

Host: %s
Expected key: %s
Received key: %s

To accept the new key, remove the old entry from:
  %s

Connection refused.`,
		e.Address,
		base64.StdEncoding.EncodeToString(e.ExpectedKey),
		base64.StdEncoding.EncodeToString(e.ActualKey),
		e.FilePath,
	)
}
