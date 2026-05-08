//go:build !darwin && !windows

package ui

func getCursorPos() point { return point{} }

func setCursorPos(p point) {}
