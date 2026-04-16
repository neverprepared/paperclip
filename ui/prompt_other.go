//go:build !darwin

package ui

func promptInput(title, message, defaultValue string) string {
	return ""
}

func promptConfirm(title, message string) bool {
	return false
}
