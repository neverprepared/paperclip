package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/flynn/noise"
	"github.com/mindmorass/paperclip/clipboard"
	"github.com/mindmorass/paperclip/crypto"
	"github.com/mindmorass/paperclip/peer"
)

var version = "0.1.0"

// Config holds the parsed command-line configuration
type Config struct {
	Port    int
	Peers   string
	PollMs  int
	Verbose bool
}

func main() {
	var (
		port       = flag.Int("port", 9999, "TCP port for peer connections")
		peers      = flag.String("peers", "", "Comma-separated list of peer addresses (host:port)")
		pollMs     = flag.Int("poll", 500, "Clipboard poll interval in milliseconds")
		showVer    = flag.Bool("version", false, "Show version")
		verbose    = flag.Bool("v", false, "Verbose logging")
		genService = flag.Bool("service", false, "Generate platform service config and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Printf("paperclip v%s\n", version)
		os.Exit(0)
	}

	config := Config{
		Port:    *port,
		Peers:   *peers,
		PollMs:  *pollMs,
		Verbose: *verbose,
	}

	if *genService {
		generateServiceConfig(config)
		os.Exit(0)
	}

	runDaemon(config)
}

func runDaemon(config Config) {
	logger := log.New(os.Stdout, "[paperclip] ", log.LstdFlags)
	if !config.Verbose {
		logger.SetOutput(os.Stderr)
	}

	// Check if any peer uses noise: prefix
	usesCrypto := strings.Contains(config.Peers, "noise:")

	var identity noise.DHKey
	var knownHosts *crypto.KnownHosts

	if usesCrypto {
		// Initialize crypto
		configDir, err := crypto.GetConfigDir()
		if err != nil {
			logger.Fatalf("Failed to get config directory: %v", err)
		}

		identity, err = crypto.LoadOrCreateIdentity(configDir)
		if err != nil {
			logger.Fatalf("Failed to load/create identity: %v", err)
		}

		knownHosts, err = crypto.LoadKnownHosts(configDir)
		if err != nil {
			logger.Fatalf("Failed to load known_hosts: %v", err)
		}

		logger.Printf("Local public key: %s", crypto.PublicKeyFull(identity.Public))
		logger.Printf("Fingerprint: %s", crypto.PublicKeyFingerprint(identity.Public))
	}

	cb := clipboard.New(logger)
	node := peer.NewNode(config.Port, config.Peers, cb, logger, config.Verbose, identity, knownHosts)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.Println("Shutting down...")
		node.Stop()
		os.Exit(0)
	}()

	logger.Printf("Starting paperclip on port %d\n", config.Port)
	if usesCrypto {
		logger.Printf("Encryption enabled for noise: prefixed peers")
	}
	if err := node.Start(config.PollMs); err != nil {
		logger.Fatalf("Failed to start: %v", err)
	}
}
