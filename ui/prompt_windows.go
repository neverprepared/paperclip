//go:build windows

package ui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const psTimeout = 30 * time.Second

// escapePSString escapes a string for safe use inside a PowerShell single-quoted
// string literal. Single quotes are doubled per PowerShell escaping rules.
func escapePSString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// promptInput shows a native Windows input dialog via PowerShell/VisualBasic.
// Returns the text the user entered, or empty string if they cancelled.
func promptInput(title, message, defaultValue string) string {
	script := fmt.Sprintf(
		`Add-Type -AssemblyName Microsoft.VisualBasic; [Microsoft.VisualBasic.Interaction]::InputBox('%s', '%s', '%s')`,
		escapePSString(message),
		escapePSString(title),
		escapePSString(defaultValue),
	)
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", script).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// promptConfirm shows a native Windows OK/Cancel dialog via PowerShell/Windows Forms.
// Returns true if the user clicks OK.
func promptConfirm(title, message string) bool {
	script := fmt.Sprintf(
		`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.MessageBox]::Show('%s', '%s', 'OKCancel') -eq 'OK'`,
		escapePSString(message),
		escapePSString(title),
	)
	ctx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", script).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "True"
}
