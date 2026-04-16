//go:build darwin

package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mindmorass/paperclip/config"
)

const (
	plistLabel = "com.github.mindmorass.paperclip"
)

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
}

func isLaunchAgentInstalled() bool {
	_, err := os.Stat(plistPath())
	return err == nil
}

func installLaunchAgent(cfg *config.Config) error {
	execPath, err := os.Executable()
	if err != nil {
		home, _ := os.UserHomeDir()
		execPath = filepath.Join(home, "bin", "paperclip")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Build room names for --ably-room flag.
	var roomNames []string
	for _, r := range cfg.Relay.EnabledRooms() {
		roomNames = append(roomNames, r.Name)
	}

	// The API key is read from the system keychain at runtime — not embedded in
	// the plist — so no sensitive credentials appear in the LaunchAgent file.
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>-poll</string>
        <string>%d</string>
        <string>-ably-room</string>
        <string>%s</string>
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
`, plistLabel, execPath, cfg.PollMs, strings.Join(roomNames, ","), home, home)

	dir := filepath.Dir(plistPath())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// 0600: plist is owner-readable only (no sensitive data, but good hygiene).
	if err := os.WriteFile(plistPath(), []byte(plist), 0600); err != nil {
		return err
	}

	return exec.Command("launchctl", "load", plistPath()).Run()
}

func uninstallLaunchAgent() error {
	if err := exec.Command("launchctl", "unload", plistPath()).Run(); err != nil {
		return fmt.Errorf("launchctl unload: %w", err)
	}
	return os.Remove(plistPath())
}
