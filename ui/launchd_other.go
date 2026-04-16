//go:build !darwin

package ui

import "github.com/mindmorass/paperclip/config"

func isLaunchAgentInstalled() bool {
	return false
}

func installLaunchAgent(cfg *config.Config) error {
	return nil
}

func uninstallLaunchAgent() error {
	return nil
}
