//go:build windows

package ui

import (
	"fmt"
	"os"

	"github.com/mindmorass/paperclip/config"
	"golang.org/x/sys/windows/registry"
)

// runKeyPath is the per-user Registry key for programs that run at login.
// HKCU requires no administrative access.
const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
const appRegName = "Paperclip"

func isLaunchAgentInstalled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(appRegName)
	return err == nil
}

func installLaunchAgent(cfg *config.Config) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine executable path: %w", err)
	}

	// Store only the executable path — no flags needed. The binary name
	// contains "tray" so it defaults to tray mode automatically, and all
	// config (clipboards, poll rate, etc.) is read from config.json at
	// startup. Hardcoding flags here would cause stale config after the
	// user makes changes via the tray UI.
	value := fmt.Sprintf(`"%s"`, execPath)

	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("could not open registry Run key: %w", err)
	}
	defer k.Close()
	return k.SetStringValue(appRegName, value)
}

func uninstallLaunchAgent() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("could not open registry Run key: %w", err)
	}
	defer k.Close()
	if err := k.DeleteValue(appRegName); err != nil {
		return fmt.Errorf("could not remove Paperclip from startup: %w", err)
	}
	return nil
}
