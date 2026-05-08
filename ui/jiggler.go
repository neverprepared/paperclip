package ui

import (
	"context"
	"math"
	"math/rand"
	"sync"
	"time"
)

type point struct{ X, Y float64 }

// Jiggler periodically moves the mouse cursor to prevent screen sleep.
type Jiggler struct {
	mu     sync.Mutex
	mode   string
	cancel context.CancelFunc
}

// Start begins jiggling in the given mode ("minimal" or "natural").
// Calling Start while already running stops the previous session first.
func (j *Jiggler) Start(mode string) {
	j.Stop()

	if mode == "" {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	j.mu.Lock()
	j.mode = mode
	j.cancel = cancel
	j.mu.Unlock()

	switch mode {
	case "minimal":
		go j.runMinimal(ctx)
	case "natural":
		go j.runNatural(ctx)
	}
}

// Stop halts the jiggler.
func (j *Jiggler) Stop() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cancel != nil {
		j.cancel()
		j.cancel = nil
	}
	j.mode = ""
}

// Mode returns the current jiggle mode ("", "minimal", or "natural").
func (j *Jiggler) Mode() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.mode
}

// runMinimal nudges the cursor 1px right then back every 60 seconds.
func (j *Jiggler) runMinimal(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pos := getCursorPos()
			setCursorPos(point{X: pos.X + 1, Y: pos.Y})
			sleep(ctx, 50*time.Millisecond)
			setCursorPos(pos)
		}
	}
}

// runNatural moves the cursor along Bézier arcs that look like real usage.
func (j *Jiggler) runNatural(ctx context.Context) {
	for {
		// 1-3 consecutive arcs per burst
		arcs := 1 + rand.Intn(3)
		for i := 0; i < arcs; i++ {
			if ctx.Err() != nil {
				return
			}
			j.doArc(ctx)
			// Brief pause between consecutive arcs (0.3-1s)
			if i < arcs-1 {
				sleep(ctx, time.Duration(300+rand.Intn(700))*time.Millisecond)
			}
		}

		// Long pause between bursts: 45-90 seconds
		pause := time.Duration(45+rand.Intn(46)) * time.Second
		if !sleep(ctx, pause) {
			return
		}
	}
}

// doArc moves the cursor along a single quadratic Bézier arc.
func (j *Jiggler) doArc(ctx context.Context) {
	start := getCursorPos()

	// Random destination within ~100-200px radius
	radius := 100.0 + rand.Float64()*100.0
	angle := rand.Float64() * 2 * math.Pi
	end := point{
		X: math.Max(0, start.X+radius*math.Cos(angle)),
		Y: math.Max(0, start.Y+radius*math.Sin(angle)),
	}

	// Control point perpendicular to start→end at 0.2-0.4x distance
	dx := end.X - start.X
	dy := end.Y - start.Y
	dist := math.Sqrt(dx*dx + dy*dy)
	if dist < 1 {
		return
	}

	perpX := -dy / dist
	perpY := dx / dist
	offset := (0.2 + rand.Float64()*0.2) * dist
	if rand.Float64() < 0.5 {
		offset = -offset
	}
	ctrl := point{
		X: start.X + dx/2 + perpX*offset,
		Y: start.Y + dy/2 + perpY*offset,
	}

	// Walk the curve with eased timing
	steps := 15 + rand.Intn(11) // 15-25 steps
	for i := 1; i <= steps; i++ {
		if ctx.Err() != nil {
			return
		}
		t := easeInOut(float64(i) / float64(steps))
		p := bezierPoint(start, ctrl, end, t)
		setCursorPos(point{X: math.Round(p.X), Y: math.Round(p.Y)})
		sleep(ctx, time.Duration(8+rand.Intn(8))*time.Millisecond)
	}
}

// bezierPoint evaluates a quadratic Bézier curve at parameter t ∈ [0,1].
func bezierPoint(p0, p1, p2 point, t float64) point {
	u := 1 - t
	return point{
		X: u*u*p0.X + 2*u*t*p1.X + t*t*p2.X,
		Y: u*u*p0.Y + 2*u*t*p1.Y + t*t*p2.Y,
	}
}

// easeInOut applies a cubic ease-in-out curve.
func easeInOut(t float64) float64 {
	if t < 0.5 {
		return 4 * t * t * t
	}
	return 1 - math.Pow(-2*t+2, 3)/2
}

// sleep returns false if the context was cancelled during the wait.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
