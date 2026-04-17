//go:build windows

package ui

import (
	"fmt"
	"os"
	"strings"

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

	var names []string
	for _, c := range cfg.Relay.EnabledClipboards() {
		names = append(names, c.Name)
	}

	// Run in tray mode at login. Clipboard names are embedded so the registry
	// entry reflects the current config (updated on next install call).
	value := fmt.Sprintf(`"%s" --tray --poll %d`, execPath, cfg.PollMs)
	if len(names) > 0 {
		value += fmt.Sprintf(` --clipboard %s`, strings.Join(names, ","))
	}

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
