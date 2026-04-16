//go:build darwin

package ui

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const osascriptTimeout = 30 * time.Second

// escapeOSAScript escapes a string for safe interpolation inside an
// AppleScript double-quoted string literal.
func escapeOSAScript(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// promptInput shows a native macOS text input dialog and returns the entered text.
// Returns empty string if the user cancels or the dialog times out.
func promptInput(title, message, defaultValue string) string {
	script := `display dialog "` + escapeOSAScript(message) + `" default answer "` + escapeOSAScript(defaultValue) + `" with title "` + escapeOSAScript(title) + `" buttons {"Cancel", "OK"} default button "OK"
set result to text returned of result
return result`

	ctx, cancel := context.WithTimeout(context.Background(), osascriptTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// promptConfirm shows a native macOS confirmation dialog.
// Returns true if the user clicks OK.
func promptConfirm(title, message string) bool {
	script := `display dialog "` + escapeOSAScript(message) + `" with title "` + escapeOSAScript(title) + `" buttons {"Cancel", "OK"} default button "OK"`

	ctx, cancel := context.WithTimeout(context.Background(), osascriptTimeout)
	defer cancel()

	err := exec.CommandContext(ctx, "osascript", "-e", script).Run()
	return err == nil
}
