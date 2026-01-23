package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/mindmorass/paperclip/clipboard"
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

	cb := clipboard.New(logger)
	node := peer.NewNode(config.Port, config.Peers, cb, logger, config.Verbose)

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
	if err := node.Start(config.PollMs); err != nil {
		logger.Fatalf("Failed to start: %v", err)
	}
}
