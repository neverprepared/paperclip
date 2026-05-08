//go:build darwin

package ui

/*
#cgo LDFLAGS: -framework CoreGraphics
#include <CoreGraphics/CoreGraphics.h>
*/
import "C"

func getCursorPos() point {
	event := C.CGEventCreate(0)
	defer C.CFRelease(C.CFTypeRef(event))
	loc := C.CGEventGetLocation(event)
	return point{X: float64(loc.x), Y: float64(loc.y)}
}

func setCursorPos(p point) {
	pt := C.CGPoint{x: C.CGFloat(p.X), y: C.CGFloat(p.Y)}
	event := C.CGEventCreateMouseEvent(0, C.kCGEventMouseMoved, pt, C.kCGMouseButtonLeft)
	defer C.CFRelease(C.CFTypeRef(event))
	C.CGEventPost(C.kCGHIDEventTap, event)
}
