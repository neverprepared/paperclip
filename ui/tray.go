package ui

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/mindmorass/paperclip/clipboard"
	"github.com/mindmorass/paperclip/config"
	"github.com/mindmorass/paperclip/relay"
)

var validRoomName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Run starts the systray menu bar UI and blocks until quit.
// newRelay is called to create (or recreate) the relay whenever config changes.
// cb is used by the auto-clear timer to wipe the clipboard after inactivity.
func Run(cfg *config.Config, cb *clipboard.Clipboard, newRelay func() *relay.Relay, onQuit func()) {
	s := &trayState{
		cfg:      cfg,
		cb:       cb,
		newRelay: newRelay,
		onQuit:   onQuit,
	}
	s.r = newRelay()
	s.startClearTimer(cfg.ClearAfterSeconds)

	systray.Run(func() { s.build() }, func() {})
}

type trayState struct {
	cfg      *config.Config
	cb       *clipboard.Clipboard
	newRelay func() *relay.Relay
	onQuit   func()

	mu          sync.Mutex
	r           *relay.Relay
	menuCancel  context.CancelFunc
	clearCancel context.CancelFunc
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

// startClearTimer cancels any existing clear timer and starts a new one.
// seconds=0 disables auto-clear.
func (s *trayState) startClearTimer(seconds int) {
	s.mu.Lock()
	if s.clearCancel != nil {
		s.clearCancel()
		s.clearCancel = nil
	}
	s.mu.Unlock()

	if seconds <= 0 || s.cb == nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.clearCancel = cancel
	s.mu.Unlock()

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		lastHash := s.cb.GetLastHash()
		lastChanged := time.Now()
		cleared := false

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current := s.cb.GetLastHash()
				if current != lastHash {
					lastHash = current
					lastChanged = time.Now()
					cleared = false
				} else if !cleared && current != "" && time.Since(lastChanged) >= time.Duration(seconds)*time.Second {
					_ = s.cb.Write(&clipboard.Content{Type: clipboard.TypeText, Data: []byte{}})
					cleared = true
				}
			}
		}
	}()
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

	statuses := map[string]relay.ClipboardStatus{}
	if r != nil {
		for _, st := range r.Status() {
			statuses[st.Name] = st
		}
	}

	if len(cfg.Relay.Clipboards) == 0 {
		mSetup := systray.AddMenuItem("⚠  Configure Paperclip...", "Set up your API key and first clipboard")
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
		for i, cb := range cfg.Relay.Clipboards {
			st := statuses[cb.Name]
			hasPass := relay.HasPassphrase(cb.Name)

			mRoom := systray.AddMenuItem(clipboardMenuLabel(cb.Name, st, hasPass, r != nil), "")

			// Connection status sub-item
			connLabel := "  ⚪  Not connected"
			if r != nil && st.Connected {
				connLabel = "  🟢  Connected"
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
			mPass := mRoom.AddSubMenuItem(passLabel, "Set encryption passphrase for this clipboard")
			mRemove := mRoom.AddSubMenuItem(fmt.Sprintf("Remove \"%s\"", cb.Name), "Remove this clipboard")

			entries = append(entries, roomEntry{
				name:         cb.Name,
				cfgIdx:       i,
				passphraseCh: mPass.ClickedCh,
				removeCh:     mRemove.ClickedCh,
			})
		}
	}

	systray.AddSeparator()

	// ── Hub mode ─────────────────────────────────────────────────────────
	mHub := systray.AddMenuItemCheckbox("  Hub Mode", "Route clipboard to specific destinations", cfg.IsHub)

	// "Broadcast to" submenu — only meaningful in hub mode
	var broadcastSubs []struct {
		name string
		item *systray.MenuItem
	}
	mBroadcast := systray.AddMenuItem("  Broadcast to...", "Choose which clipboards receive your copies")
	if !cfg.IsHub {
		mBroadcast.Hide()
	}
	// One checkbox per clipboard
	allNames := make([]string, len(cfg.Relay.Clipboards))
	for i, c := range cfg.Relay.Clipboards {
		allNames[i] = c.Name
	}
	mBcastAll := mBroadcast.AddSubMenuItem("All Clipboards", "Send to every connected clipboard")
	mBroadcast.AddSeparator()
	if len(cfg.HubTargets) == 0 {
		mBcastAll.Check()
	}
	for _, name := range allNames {
		name := name
		checked := false
		if len(cfg.HubTargets) > 0 {
			for _, t := range cfg.HubTargets {
				if t == name {
					checked = true
					break
				}
			}
		}
		sub := mBroadcast.AddSubMenuItemCheckbox(name, "", checked)
		broadcastSubs = append(broadcastSubs, struct {
			name string
			item *systray.MenuItem
		}{name, sub})
	}

	systray.AddSeparator()
	mAddRoom := systray.AddMenuItem("  Add Clipboard...", "Add a new sync clipboard")
	mSetKey := systray.AddMenuItem("  Set API Key...", "Update your Ably API key")

	systray.AddSeparator()

	// ── Auto-clear submenu ────────────────────────────────────────────────
	clearOptions := []struct {
		label   string
		seconds int
	}{
		{"Off", 0},
		{"5 seconds", 5},
		{"10 seconds", 10},
		{"15 seconds", 15},
		{"20 seconds", 20},
		{"30 seconds", 30},
		{"40 seconds", 40},
		{"50 seconds", 50},
		{"60 seconds", 60},
		{"Custom...", -1},
	}
	current := cfg.ClearAfterSeconds
	clearLabel := "Auto-clear: Off"
	for _, o := range clearOptions {
		if o.seconds == current && o.seconds >= 0 {
			clearLabel = fmt.Sprintf("Auto-clear: %s", o.label)
		}
	}
	if current > 0 {
		found := false
		for _, o := range clearOptions {
			if o.seconds == current {
				found = true
				break
			}
		}
		if !found {
			clearLabel = fmt.Sprintf("Auto-clear: %ds", current)
		}
	}
	mClear := systray.AddMenuItem(clearLabel, "Automatically clear clipboard after inactivity")
	var clearSubs []*systray.MenuItem
	for _, o := range clearOptions {
		checked := o.seconds == current || (o.seconds == -1 && current > 0 && func() bool {
			for _, x := range clearOptions {
				if x.seconds == current {
					return false
				}
			}
			return true
		}())
		sub := mClear.AddSubMenuItem(o.label, "")
		if checked {
			sub.Check()
		}
		clearSubs = append(clearSubs, sub)
	}

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
					pass := promptPassphrase(fmt.Sprintf("Enter passphrase for clipboard \"%s\".\nAll devices sharing this clipboard must use the same passphrase.", e.name))
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
					if !promptConfirm("Remove Clipboard", fmt.Sprintf("Remove clipboard \"%s\" and delete its passphrase?", e.name)) {
						continue
					}
					idx := e.cfgIdx
					if idx >= 0 && idx < len(cfg.Relay.Clipboards) {
						cfg.Relay.Clipboards = append(cfg.Relay.Clipboards[:idx], cfg.Relay.Clipboards[idx+1:]...)
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

	// applyHubFilter pushes the current hub config to the relay without restart.
	applyHubFilter := func() {
		r := s.relay()
		if r == nil {
			return
		}
		if !cfg.IsHub || len(cfg.HubTargets) == 0 {
			r.SetPublishFilter(nil)
		} else {
			r.SetPublishFilter(cfg.HubTargets)
		}
	}

	// Hub mode toggle
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-mHub.ClickedCh:
				if !ok {
					return
				}
				cfg.IsHub = !cfg.IsHub
				if err := config.Save(cfg); err != nil {
					promptConfirm("Error", fmt.Sprintf("Failed to save config: %v", err))
					continue
				}
				applyHubFilter()
				s.build()
				return
			}
		}
	}()

	// "All Clipboards" broadcast option
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-mBcastAll.ClickedCh:
				if !ok {
					return
				}
				cfg.HubTargets = nil
				if err := config.Save(cfg); err != nil {
					promptConfirm("Error", fmt.Sprintf("Failed to save config: %v", err))
					continue
				}
				applyHubFilter()
				s.build()
				return
			}
		}
	}()

	// Per-clipboard broadcast toggles
	for _, bs := range broadcastSubs {
		bs := bs
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-bs.item.ClickedCh:
					if !ok {
						return
					}
					// Toggle this clipboard in HubTargets.
					found := false
					for i, t := range cfg.HubTargets {
						if t == bs.name {
							cfg.HubTargets = append(cfg.HubTargets[:i], cfg.HubTargets[i+1:]...)
							found = true
							break
						}
					}
					if !found {
						cfg.HubTargets = append(cfg.HubTargets, bs.name)
					}
					// If nothing selected, fall back to "all".
					if len(cfg.HubTargets) == 0 {
						cfg.HubTargets = nil
					}
					if err := config.Save(cfg); err != nil {
						promptConfirm("Error", fmt.Sprintf("Failed to save config: %v", err))
						continue
					}
					applyHubFilter()
					s.build()
					return
				}
			}
		}()
	}

	// Add clipboard
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

	// Auto-clear options
	for i, sub := range clearSubs {
		i, sub := i, sub
		opt := clearOptions[i]
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-sub.ClickedCh:
					if !ok {
						return
					}
					seconds := opt.seconds
					if seconds == -1 {
						// Custom
						raw := promptInput("Auto-clear", "Clear clipboard after how many seconds? (0 to disable):", fmt.Sprintf("%d", cfg.ClearAfterSeconds))
						n, err := strconv.Atoi(strings.TrimSpace(raw))
						if err != nil || n < 0 {
							continue
						}
						seconds = n
					}
					cfg.ClearAfterSeconds = seconds
					if err := config.Save(cfg); err != nil {
						promptConfirm("Error", fmt.Sprintf("Failed to save config: %v", err))
						continue
					}
					s.startClearTimer(seconds)
					s.build()
					return
				}
			}
		}()
	}

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
	if len(s.cfg.Relay.Clipboards) == 0 {
		return "⚠️  Not configured"
	}
	if r == nil {
		return "⚪  Not connected"
	}
	if r.Connected() {
		n := len(r.ClipboardNames())
		suffix := ""
		if s.cfg.IsHub {
			suffix = " · hub"
		}
		if n == 1 {
			return fmt.Sprintf("🟢  Connected · 1 clipboard%s", suffix)
		}
		return fmt.Sprintf("🟢  Connected · %d clipboards%s", n, suffix)
	}
	return "⚪  Connecting..."
}

func (s *trayState) tooltipText(r *relay.Relay) string {
	if r == nil || !r.Connected() {
		return "Paperclip — not connected"
	}
	rooms := r.ClipboardNames()
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

func clipboardMenuLabel(name string, st relay.ClipboardStatus, hasPass bool, relayRunning bool) string {
	dot := "⚪"
	if relayRunning && st.Connected {
		dot = "🟢"
	}
	label := fmt.Sprintf("  %s  %s", dot, name)
	if hasPass {
		label += " 🔒"
	}
	return label
}

// runSetupFlow walks a first-run user through API key + first clipboard setup.
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

// runAddRoomFlow prompts for a clipboard name + passphrase, persists both.
// Returns true if a clipboard was successfully added.
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

	name := strings.TrimSpace(promptInput("Add Clipboard", "Enter a clipboard name (letters, numbers, dash, underscore):", ""))
	if name == "" {
		return false
	}
	if !validRoomName.MatchString(name) {
		promptConfirm("Invalid Clipboard Name", "Clipboard name may only contain letters, numbers, - and _.")
		return false
	}

	pass := promptPassphrase(fmt.Sprintf("Set passphrase for clipboard \"%s\".\nAll devices sharing this clipboard must use the same passphrase.", name))
	if pass == "" {
		return false
	}

	if err := relay.SetPassphrase(name, pass); err != nil {
		promptConfirm("Error", fmt.Sprintf("Failed to save passphrase: %v", err))
		return false
	}

	s.cfg.Relay.Clipboards = append(s.cfg.Relay.Clipboards, config.Clipboard{Name: name, Enabled: true})
	if err := config.Save(s.cfg); err != nil {
		promptConfirm("Error", fmt.Sprintf("Failed to save config: %v", err))
		return false
	}
	return true
}

// promptPassphrase loops until the user enters a valid passphrase or cancels.
func promptPassphrase(message string) string {
	for {
		pass := promptInput("Clipboard Passphrase", message, "")
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
