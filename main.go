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

var version = "0.4.0"

func main() {
	var (
		pollMs  = flag.Int("poll", 0, "Clipboard poll interval in milliseconds")
		showVer = flag.Bool("version", false, "Show version")
		verbose = flag.Bool("v", false, "Verbose logging")
		tray    = flag.Bool("tray", false, "Run with menu bar UI")
		clipboardName = flag.String("clipboard", "", "Comma-separated clipboard names")
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

	if *clipboardName != "" {
		rooms := strings.Split(*clipboardName, ",")
		cfg.Relay.Clipboards = nil
		for _, r := range rooms {
			r = strings.TrimSpace(r)
			if r != "" {
				cfg.Relay.Clipboards = append(cfg.Relay.Clipboards, config.Clipboard{Name: r, Enabled: true})
			}
		}
	}

	// Default to tray mode when the binary name contains "tray"
	// (e.g. paperclip-tray.exe) so double-clicking it just works.
	if *tray || strings.Contains(strings.ToLower(os.Args[0]), "tray") {
		runTray(cfg)
	} else {
		runDaemon(cfg, apiKey)
	}
}

func startRelay(cfg *config.Config, apiKey string, cb *clipboard.Clipboard, logger *log.Logger, verbose bool) *relay.Relay {
	enabledClipboards := cfg.Relay.EnabledClipboards()
	if apiKey == "" || len(enabledClipboards) == 0 {
		return nil
	}

	var clipboardNames []string
	for _, r := range enabledClipboards {
		clipboardNames = append(clipboardNames, r.Name)
	}

	r, err := relay.New(apiKey, clipboardNames, cb, logger, verbose)
	if err != nil {
		logger.Printf("Failed to create relay: %v", err)
		return nil
	}

	if err := r.Start(cfg.PollMs); err != nil {
		logger.Printf("Failed to start relay: %v", err)
		return nil
	}

	// Apply hub publish filter from config.
	if cfg.IsHub {
		r.SetPublishFilter(cfg.HubTargets)
	}

	return r
}

func runTray(cfg *config.Config) {
	logger := log.New(os.Stdout, "[paperclip] ", log.LstdFlags)
	cb := clipboard.New(logger)

	// newRelay reads the API key from keychain each time so that key updates
	// via the tray take effect without restarting the process.
	newRelay := func() *relay.Relay {
		key, _ := relay.GetAPIKey()
		if key == "" {
			key = os.Getenv("PAPERCLIP_ABLY_KEY")
		}
		return startRelay(cfg, key, cb, logger, cfg.Verbose)
	}

	logger.Println("Starting paperclip (tray mode)")
	ui.Run(cfg, cb, newRelay, func() {
		logger.Println("Shutting down...")
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
		logger.Fatal("No relay configured. Set up an Ably API key and clipboards via --tray, or set PAPERCLIP_ABLY_KEY.")
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	logger.Println("Starting paperclip")
	<-sigChan
	logger.Println("Shutting down...")
	r.Stop()
}
