package ui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/mindmorass/paperclip/config"
	"github.com/mindmorass/paperclip/relay"
)

var validRoomName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Run starts the systray menu bar UI and blocks until quit.
// newRelay is called to create (or recreate) the relay whenever config changes.
func Run(cfg *config.Config, newRelay func() *relay.Relay, onQuit func()) {
	s := &trayState{
		cfg:      cfg,
		newRelay: newRelay,
		onQuit:   onQuit,
	}
	s.r = newRelay()

	systray.Run(func() { s.build() }, func() {})
}

type trayState struct {
	cfg      *config.Config
	newRelay func() *relay.Relay
	onQuit   func()

	mu         sync.Mutex
	r          *relay.Relay
	menuCancel context.CancelFunc
}

func (s *trayState) relay() *relay.Relay {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.r
}

// restart stops the current relay and starts a fresh one from the factory.
func (s *trayState) restart() {
	s.mu.Lock()
	old := s.r
	s.mu.Unlock()

	if old != nil {
		old.Stop()
	}

	r := s.newRelay()

	s.mu.Lock()
	s.r = r
	s.mu.Unlock()
}

// build (re)constructs the entire tray menu. Safe to call from any goroutine.
// It cancels goroutines from the previous build before resetting the menu.
func (s *trayState) build() {
	s.mu.Lock()
	if s.menuCancel != nil {
		s.menuCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.menuCancel = cancel
	s.mu.Unlock()

	systray.ResetMenu()

	r := s.relay()
	cfg := s.cfg

	systray.SetTemplateIcon(iconData, iconDataRetina)
	systray.SetTooltip(s.tooltipText(r))

	// ── Status ───────────────────────────────────────────────────────────
	mStatus := systray.AddMenuItem(s.statusText(r), "")
	mStatus.Disable()

	mLastSync := systray.AddMenuItem("", "")
	mLastSync.Disable()
	mLastSync.Hide()

	systray.AddSeparator()

	// ── Rooms ─────────────────────────────────────────────────────────────
	type roomEntry struct {
		name         string
		cfgIdx       int
		passphraseCh <-chan struct{}
		removeCh     <-chan struct{}
	}
	var entries []roomEntry

	statuses := map[string]relay.RoomStatus{}
	if r != nil {
		for _, st := range r.Status() {
			statuses[st.Name] = st
		}
	}

	if len(cfg.Relay.Rooms) == 0 {
		mSetup := systray.AddMenuItem("⚠  Configure Paperclip...", "Set up your API key and first room")
		go func() {
			select {
			case <-ctx.Done():
			case <-mSetup.ClickedCh:
				if s.runSetupFlow() {
					s.restart()
					s.build()
				}
			}
		}()
	} else {
		for i, room := range cfg.Relay.Rooms {
			st := statuses[room.Name]
			hasPass := relay.HasPassphrase(room.Name)

			mRoom := systray.AddMenuItem(roomMenuLabel(room.Name, st, hasPass, r != nil), "")

			// Connection status sub-item
			connLabel := "  ○  Not connected"
			if r != nil && st.Connected {
				connLabel = "  ●  Connected"
			} else if r == nil {
				connLabel = "  —  Daemon not running"
			}
			mConn := mRoom.AddSubMenuItem(connLabel, "")
			mConn.Disable()
			mRoom.AddSeparator()

			passLabel := "Set Passphrase..."
			if hasPass {
				passLabel = "Change Passphrase..."
			}
			mPass := mRoom.AddSubMenuItem(passLabel, "Set encryption passphrase for this room")
			mRemove := mRoom.AddSubMenuItem(fmt.Sprintf("Remove \"%s\"", room.Name), "Remove this room")

			entries = append(entries, roomEntry{
				name:         room.Name,
				cfgIdx:       i,
				passphraseCh: mPass.ClickedCh,
				removeCh:     mRemove.ClickedCh,
			})
		}
	}

	systray.AddSeparator()
	mAddRoom := systray.AddMenuItem("  Add Room...", "Add a new sync room")
	mSetKey := systray.AddMenuItem("  Set API Key...", "Update your Ably API key")

	systray.AddSeparator()
	mLogin := systray.AddMenuItemCheckbox("Start at Login", "Launch Paperclip automatically at login", isLaunchAgentInstalled())

	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit Paperclip", "")

	// ── Event goroutines ──────────────────────────────────────────────────

	for _, e := range entries {
		e := e
		// Passphrase
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-e.passphraseCh:
					if !ok {
						return
					}
					pass := promptPassphrase(fmt.Sprintf("Enter passphrase for room \"%s\".\nAll devices sharing this room must use the same passphrase.", e.name))
					if pass == "" {
						continue
					}
					if err := relay.SetPassphrase(e.name, pass); err != nil {
						promptConfirm("Error", fmt.Sprintf("Failed to save passphrase: %v", err))
						continue
					}
					s.restart()
					s.build()
					return
				}
			}
		}()
		// Remove
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-e.removeCh:
					if !ok {
						return
					}
					if !promptConfirm("Remove Room", fmt.Sprintf("Remove room \"%s\" and delete its passphrase?", e.name)) {
						continue
					}
					idx := e.cfgIdx
					if idx >= 0 && idx < len(cfg.Relay.Rooms) {
						cfg.Relay.Rooms = append(cfg.Relay.Rooms[:idx], cfg.Relay.Rooms[idx+1:]...)
					}
					relay.DeletePassphrase(e.name)
					if err := config.Save(cfg); err != nil {
						promptConfirm("Error", fmt.Sprintf("Failed to save config: %v", err))
						return
					}
					s.restart()
					s.build()
					return
				}
			}
		}()
	}

	// Add room
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-mAddRoom.ClickedCh:
				if !ok {
					return
				}
				if s.runAddRoomFlow() {
					s.restart()
					s.build()
					return
				}
			}
		}
	}()

	// Set API key
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-mSetKey.ClickedCh:
				if !ok {
					return
				}
				apiKey := promptInput("Ably API Key", "Enter your Ably API key:", "")
				if apiKey == "" {
					continue
				}
				if err := relay.SetAPIKey(apiKey); err != nil {
					promptConfirm("Invalid API Key", fmt.Sprintf("%v", err))
					continue
				}
				s.restart()
				s.build()
				return
			}
		}
	}()

	// Login toggle
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-mLogin.ClickedCh:
				if !ok {
					return
				}
				if mLogin.Checked() {
					mLogin.Uncheck()
					if err := uninstallLaunchAgent(); err != nil {
						promptConfirm("Warning", fmt.Sprintf("Failed to remove launch agent: %v", err))
					}
				} else {
					mLogin.Check()
					installLaunchAgent(cfg)
				}
			}
		}
	}()

	// Quit
	go func() {
		select {
		case <-ctx.Done():
		case <-mQuit.ClickedCh:
			s.mu.Lock()
			s.menuCancel()
			s.mu.Unlock()
			if s.onQuit != nil {
				s.onQuit()
			}
			if r := s.relay(); r != nil {
				r.Stop()
			}
			systray.Quit()
		}
	}()

	// Status ticker
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r := s.relay()
				mStatus.SetTitle(s.statusText(r))
				systray.SetTooltip(s.tooltipText(r))
				updateLastSync(r, mLastSync)
			}
		}
	}()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (s *trayState) statusText(r *relay.Relay) string {
	if len(s.cfg.Relay.Rooms) == 0 {
		return "⚠  Not configured"
	}
	if r == nil {
		return "○  Not connected"
	}
	if r.Connected() {
		n := len(r.RoomNames())
		if n == 1 {
			return "●  Connected · 1 room"
		}
		return fmt.Sprintf("●  Connected · %d rooms", n)
	}
	return "○  Connecting..."
}

func (s *trayState) tooltipText(r *relay.Relay) string {
	if r == nil || !r.Connected() {
		return "Paperclip — not connected"
	}
	rooms := r.RoomNames()
	if len(rooms) == 0 {
		return "Paperclip — connected"
	}
	return fmt.Sprintf("Paperclip — %s", strings.Join(rooms, ", "))
}

func updateLastSync(r *relay.Relay, item *systray.MenuItem) {
	if r == nil {
		item.Hide()
		return
	}
	t := r.LastSyncAt()
	if t.IsZero() {
		item.Hide()
		return
	}
	elapsed := time.Since(t)
	var label string
	switch {
	case elapsed < time.Minute:
		label = fmt.Sprintf("  Last sync: %ds ago", int(elapsed.Seconds()))
	case elapsed < time.Hour:
		label = fmt.Sprintf("  Last sync: %dm ago", int(elapsed.Minutes()))
	default:
		label = fmt.Sprintf("  Last sync: %dh ago", int(elapsed.Hours()))
	}
	item.SetTitle(label)
	item.Show()
}

func roomMenuLabel(name string, st relay.RoomStatus, hasPass bool, relayRunning bool) string {
	dot := "○"
	if relayRunning && st.Connected {
		dot = "●"
	}
	label := fmt.Sprintf("  %s  %s", dot, name)
	if hasPass {
		label += " 🔒"
	}
	return label
}

// runSetupFlow walks a first-run user through API key + first room setup.
func (s *trayState) runSetupFlow() bool {
	apiKey := promptInput("Welcome to Paperclip", "Enter your Ably API key to get started:", "")
	if apiKey == "" {
		return false
	}
	if err := relay.SetAPIKey(apiKey); err != nil {
		promptConfirm("Invalid API Key", fmt.Sprintf("%v", err))
		return false
	}
	return s.runAddRoomFlow()
}

// runAddRoomFlow prompts for a room name + passphrase, persists both.
// Returns true if a room was successfully added.
func (s *trayState) runAddRoomFlow() bool {
	// Ensure API key exists first.
	if apiKey, err := relay.GetAPIKey(); err != nil || apiKey == "" {
		newKey := promptInput("Ably API Key Required", "Enter your Ably API key:", "")
		if newKey == "" {
			return false
		}
		if err := relay.SetAPIKey(newKey); err != nil {
			promptConfirm("Invalid API Key", fmt.Sprintf("%v", err))
			return false
		}
	}

	room := strings.TrimSpace(promptInput("Add Room", "Enter a room name (letters, numbers, dash, underscore):", ""))
	if room == "" {
		return false
	}
	if !validRoomName.MatchString(room) {
		promptConfirm("Invalid Room Name", "Room name may only contain letters, numbers, - and _.")
		return false
	}

	pass := promptPassphrase(fmt.Sprintf("Set passphrase for \"%s\".\nAll devices sharing this room must use the same passphrase.", room))
	if pass == "" {
		return false
	}

	if err := relay.SetPassphrase(room, pass); err != nil {
		promptConfirm("Error", fmt.Sprintf("Failed to save passphrase: %v", err))
		return false
	}

	s.cfg.Relay.Rooms = append(s.cfg.Relay.Rooms, config.Room{Name: room, Enabled: true})
	if err := config.Save(s.cfg); err != nil {
		promptConfirm("Error", fmt.Sprintf("Failed to save config: %v", err))
		return false
	}
	return true
}

// promptPassphrase loops until the user enters a valid passphrase or cancels.
func promptPassphrase(message string) string {
	for {
		pass := promptInput("Room Passphrase", message, "")
		if pass == "" {
			if promptConfirm("Cancel", "No passphrase entered — cancel?") {
				return ""
			}
			continue
		}
		if len(pass) < 8 {
			promptConfirm("Too Short", "Passphrase must be at least 8 characters.")
			continue
		}
		return pass
	}
}
