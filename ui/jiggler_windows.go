//go:build windows

package ui

import (
	"syscall"
	"unsafe"
)

var (
	modUser32     = syscall.NewLazyDLL("user32.dll")
	pGetCursorPos = modUser32.NewProc("GetCursorPos")
	pSetCursorPos = modUser32.NewProc("SetCursorPos")
)

type winPOINT struct {
	X int32
	Y int32
}

func getCursorPos() point {
	var pt winPOINT
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	return point{X: float64(pt.X), Y: float64(pt.Y)}
}

func setCursorPos(p point) {
	pSetCursorPos.Call(uintptr(int32(p.X)), uintptr(int32(p.Y)))
}
