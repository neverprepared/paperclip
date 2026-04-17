//go:build darwin

package ui

import "fyne.io/systray"

// setTrayIcon sets the system tray icon using macOS template images,
// which automatically adapt to light/dark mode.
func setTrayIcon() {
	systray.SetTemplateIcon(iconData, iconDataRetina)
}
