//go:build !darwin

package ui

import "fyne.io/systray"

// setTrayIcon sets the system tray icon using a plain PNG.
// SetIcon is used instead of SetTemplateIcon since template icons
// are a macOS-only concept.
func setTrayIcon() {
	systray.SetIcon(iconData)
}
