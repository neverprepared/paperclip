package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/mindmorass/paperclip/clipboard"
	"github.com/mindmorass/paperclip/peer"
)

var version = "0.1.0"

func main() {
	var (
		port       = flag.Int("port", 9999, "TCP port for peer connections")
		peers      = flag.String("peers", "", "Comma-separated list of peer addresses (host:port)")
		pollMs     = flag.Int("poll", 500, "Clipboard poll interval in milliseconds")
		showVer    = flag.Bool("version", false, "Show version")
		verbose    = flag.Bool("v", false, "Verbose logging")
		genLaunchd = flag.Bool("launchd", false, "Generate launchd plist and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Printf("paperclip v%s\n", version)
		os.Exit(0)
	}

	if *genLaunchd {
		generateLaunchdPlist(*port, *peers, *pollMs)
		os.Exit(0)
	}

	logger := log.New(os.Stdout, "[paperclip] ", log.LstdFlags)
	if !*verbose {
		logger.SetOutput(os.Stderr)
	}

	cb := clipboard.New(logger)
	node := peer.NewNode(*port, *peers, cb, logger, *verbose)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.Println("Shutting down...")
		node.Stop()
		os.Exit(0)
	}()

	logger.Printf("Starting paperclip on port %d\n", *port)
	if err := node.Start(*pollMs); err != nil {
		logger.Fatalf("Failed to start: %v", err)
	}
}

func generateLaunchdPlist(port int, peers string, pollMs int) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting home directory: %v\n", err)
		os.Exit(1)
	}

	execPath, err := os.Executable()
	if err != nil || execPath == "" {
		execPath = filepath.Join(homeDir, "bin", "paperclip")
	}

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.github.mindmorass.paperclip</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>-port</string>
        <string>%d</string>
        <string>-peers</string>
        <string>%s</string>
        <string>-poll</string>
        <string>%d</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s/Library/Logs/paperclip.log</string>
    <key>StandardErrorPath</key>
    <string>%s/Library/Logs/paperclip.err</string>
</dict>
</plist>
`, execPath, port, peers, pollMs, homeDir, homeDir)

	// Create LaunchAgents directory if it doesn't exist
	launchAgentsDir := filepath.Join(homeDir, "Library", "LaunchAgents")
	if err := os.MkdirAll(launchAgentsDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating LaunchAgents directory: %v\n", err)
		os.Exit(1)
	}

	// Write the plist file
	plistPath := filepath.Join(launchAgentsDir, "com.github.mindmorass.paperclip.plist")
	if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing plist file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Wrote launchd plist to: %s\n", plistPath)
	fmt.Println()
	fmt.Println("To load the service:")
	fmt.Printf("  launchctl bootstrap gui/$(id -u) %s\n", plistPath)
	fmt.Println()
	fmt.Println("To unload the service:")
	fmt.Println("  launchctl bootout gui/$(id -u)/com.github.mindmorass.paperclip")
}
