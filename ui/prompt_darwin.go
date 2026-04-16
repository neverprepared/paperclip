//go:build darwin

package ui

import (
	"os/exec"
	"strings"
)

// escapeOSAScript escapes a string for safe interpolation inside an
// AppleScript double-quoted string literal.
func escapeOSAScript(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// promptInput shows a native macOS text input dialog and returns the entered text.
// Returns empty string if the user cancels.
func promptInput(title, message, defaultValue string) string {
	script := `display dialog "` + escapeOSAScript(message) + `" default answer "` + escapeOSAScript(defaultValue) + `" with title "` + escapeOSAScript(title) + `" buttons {"Cancel", "OK"} default button "OK"
set result to text returned of result
return result`

	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// promptConfirm shows a native macOS confirmation dialog.
// Returns true if the user clicks OK.
func promptConfirm(title, message string) bool {
	script := `display dialog "` + escapeOSAScript(message) + `" with title "` + escapeOSAScript(title) + `" buttons {"Cancel", "OK"} default button "OK"`
	err := exec.Command("osascript", "-e", script).Run()
	return err == nil
}
