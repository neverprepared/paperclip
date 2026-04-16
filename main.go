package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mindmorass/paperclip/clipboard"
	"github.com/mindmorass/paperclip/config"
	"github.com/mindmorass/paperclip/relay"
	"github.com/mindmorass/paperclip/ui"
)

var version = "0.2.0"

func main() {
	var (
		pollMs  = flag.Int("poll", 0, "Clipboard poll interval in milliseconds")
		showVer = flag.Bool("version", false, "Show version")
		verbose = flag.Bool("v", false, "Verbose logging")
		tray    = flag.Bool("tray", false, "Run with menu bar UI")
		ablyRoom = flag.String("ably-room", "", "Comma-separated Ably room names")
	)
	flag.Parse()

	if *showVer {
		fmt.Printf("paperclip v%s\n", version)
		os.Exit(0)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Printf("Warning: could not load config (%v), using defaults", err)
	}

	if *pollMs != 0 {
		cfg.PollMs = *pollMs
	}
	if *verbose {
		cfg.Verbose = true
	}

	// Resolve Ably API key: keychain → env var (for CI/scripting).
	apiKey, keychainErr := relay.GetAPIKey()
	if keychainErr != nil {
		if envKey := os.Getenv("PAPERCLIP_ABLY_KEY"); envKey != "" {
			apiKey = envKey
		}
	}

	if *ablyRoom != "" {
		rooms := strings.Split(*ablyRoom, ",")
		cfg.Relay.Rooms = nil
		for _, r := range rooms {
			r = strings.TrimSpace(r)
			if r != "" {
				cfg.Relay.Rooms = append(cfg.Relay.Rooms, config.Room{Name: r, Enabled: true})
			}
		}
	}

	if *tray {
		runTray(cfg, apiKey)
	} else {
		runDaemon(cfg, apiKey)
	}
}

func startRelay(cfg *config.Config, apiKey string, cb *clipboard.Clipboard, logger *log.Logger, verbose bool) *relay.Relay {
	enabledRooms := cfg.Relay.EnabledRooms()
	if apiKey == "" || len(enabledRooms) == 0 {
		return nil
	}

	var roomNames []string
	for _, r := range enabledRooms {
		roomNames = append(roomNames, r.Name)
	}

	r, err := relay.New(apiKey, roomNames, cb, logger, verbose)
	if err != nil {
		logger.Printf("Failed to create relay: %v", err)
		return nil
	}

	if err := r.Start(cfg.PollMs); err != nil {
		logger.Printf("Failed to start relay: %v", err)
		return nil
	}

	return r
}

func runTray(cfg *config.Config, apiKey string) {
	logger := log.New(os.Stdout, "[paperclip] ", log.LstdFlags)

	cb := clipboard.New(logger)
	r := startRelay(cfg, apiKey, cb, logger, cfg.Verbose)

	logger.Println("Starting paperclip (tray mode)")

	ui.Run(r, cfg, func() {
		logger.Println("Shutting down...")
		if r != nil {
			r.Stop()
		}
	})
}

func runDaemon(cfg *config.Config, apiKey string) {
	logger := log.New(os.Stdout, "[paperclip] ", log.LstdFlags)
	if !cfg.Verbose {
		logger.SetOutput(os.Stderr)
	}

	cb := clipboard.New(logger)
	r := startRelay(cfg, apiKey, cb, logger, cfg.Verbose)

	if r == nil {
		logger.Fatal("No relay configured. Set up an Ably API key and rooms via --tray, or set PAPERCLIP_ABLY_KEY.")
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	logger.Println("Starting paperclip")
	<-sigChan
	logger.Println("Shutting down...")
	r.Stop()
}
