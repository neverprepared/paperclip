package ui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"fyne.io/systray"
	"github.com/mindmorass/paperclip/config"
	"github.com/mindmorass/paperclip/relay"
)

// validRoomName matches Ably channel naming rules: alphanumeric, dash, underscore.
var validRoomName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Run starts the systray menu bar UI and blocks until quit.
func Run(r *relay.Relay, cfg *config.Config, onQuit func()) {
	systray.Run(func() {
		onReady(r, cfg, onQuit)
	}, func() {
		// onExit
	})
}

func onReady(r *relay.Relay, cfg *config.Config, onQuit func()) {
	systray.SetTemplateIcon(iconData, iconDataRetina)
	systray.SetTooltip("Paperclip - Clipboard Sync")

	// Header
	mTitle := systray.AddMenuItem("Paperclip", "Clipboard Sync")
	mTitle.Disable()

	systray.AddSeparator()

	// --- Rooms section ---
	mRoomsHeader := systray.AddMenuItem("Rooms", "Ably sync rooms")
	mRoomsHeader.Disable()

	type roomMenuItem struct {
		statusItem     *systray.MenuItem
		passphraseItem *systray.MenuItem
		removeItem     *systray.MenuItem
		name           string
		index          int
	}

	var roomMenuItems []roomMenuItem

	if r != nil {
		statuses := r.Status()
		for _, s := range statuses {
			label := formatRoomStatus(s)
			mStatus := systray.AddMenuItem(label, s.Name)
			mStatus.Disable()

			cfgIdx := -1
			for j, cr := range cfg.Relay.Rooms {
				if cr.Name == s.Name {
					cfgIdx = j
					break
				}
			}

			passphraseLabel := "    Set Passphrase..."
			if s.Encrypted {
				passphraseLabel = "    Change Passphrase..."
			}
			mPassphrase := systray.AddMenuItem(passphraseLabel, "Set encryption passphrase for this room")
			mRemove := systray.AddMenuItem(fmt.Sprintf("    Remove %s", s.Name), "Remove this room")

			roomMenuItems = append(roomMenuItems, roomMenuItem{
				statusItem:     mStatus,
				passphraseItem: mPassphrase,
				removeItem:     mRemove,
				name:           s.Name,
				index:          cfgIdx,
			})
		}
	} else if len(cfg.Relay.Rooms) > 0 {
		for i, room := range cfg.Relay.Rooms {
			hasPass := relay.HasPassphrase(room.Name)
			label := fmt.Sprintf("  ○ %s", room.Name)
			if hasPass {
				label += " 🔒"
			}
			if !room.Enabled {
				label += " (disabled)"
			}
			mStatus := systray.AddMenuItem(label, room.Name)
			mStatus.Disable()

			passphraseLabel := "    Set Passphrase..."
			if hasPass {
				passphraseLabel = "    Change Passphrase..."
			}
			mPassphrase := systray.AddMenuItem(passphraseLabel, "Set encryption passphrase for this room")
			mRemove := systray.AddMenuItem(fmt.Sprintf("    Remove %s", room.Name), "Remove this room")

			roomMenuItems = append(roomMenuItems, roomMenuItem{
				statusItem:     mStatus,
				passphraseItem: mPassphrase,
				removeItem:     mRemove,
				name:           room.Name,
				index:          i,
			})
		}
	} else {
		mNoRooms := systray.AddMenuItem("  No rooms configured", "")
		mNoRooms.Disable()
	}

	mAddRoom := systray.AddMenuItem("  Add Room...", "Add a new sync room")
	mSetKey := systray.AddMenuItem("  Set API Key...", "Configure Ably API key")

	systray.AddSeparator()

	// Config display
	mPoll := systray.AddMenuItem(fmt.Sprintf("Poll: %dms", cfg.PollMs), "Clipboard poll interval")
	mPoll.Disable()

	// Start at login
	mLogin := systray.AddMenuItemCheckbox("Start at Login", "Manage launchd agent", isLaunchAgentInstalled())

	systray.AddSeparator()

	// Quit
	mQuit := systray.AddMenuItem("Quit", "Quit Paperclip")

	// Status update loop
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if r != nil {
				roomStatuses := r.Status()
				for i, s := range roomStatuses {
					if i < len(roomMenuItems) {
						roomMenuItems[i].statusItem.SetTitle(formatRoomStatus(s))
					}
				}
			}
		}
	}()

	// Merged channels for clicks
	roomRemoveCh := make(chan int, 10)
	roomPassphraseCh := make(chan int, 10)
	for _, rm := range roomMenuItems {
		go func(idx int, ch <-chan struct{}) {
			for range ch {
				select {
				case roomRemoveCh <- idx:
				default:
				}
			}
		}(rm.index, rm.removeItem.ClickedCh)
		go func(idx int, ch <-chan struct{}) {
			for range ch {
				select {
				case roomPassphraseCh <- idx:
				default:
				}
			}
		}(rm.index, rm.passphraseItem.ClickedCh)
	}

	// Event loop
	go func() {
		for {
			select {
			case idx := <-roomPassphraseCh:
				if idx >= 0 && idx < len(roomMenuItems) {
					name := roomMenuItems[idx].name
					pass := promptPassphrase(fmt.Sprintf("Enter new passphrase for room '%s':", name))
					if pass == "" {
						continue
					}
					if err := relay.SetPassphrase(name, pass); err != nil {
						promptConfirm("Error", fmt.Sprintf("Failed to save passphrase: %v", err))
					} else {
						promptConfirm("Passphrase Saved", fmt.Sprintf("Passphrase for '%s' saved. Restart Paperclip to apply.", name))
					}
				}
			case <-mAddRoom.ClickedCh:
				// Ensure API key is configured.
				if apiKey, err := relay.GetAPIKey(); err != nil || apiKey == "" {
					newKey := promptInput("Ably API Key", "Enter your Ably API key first:", "")
					if newKey == "" {
						continue
					}
					if err := relay.SetAPIKey(newKey); err != nil {
						promptConfirm("Error", fmt.Sprintf("Failed to save API key: %v", err))
						continue
					}
				}

				// Validate room name.
				room := strings.TrimSpace(promptInput("Add Room", "Enter a room name (letters, numbers, dash, underscore):", ""))
				if room == "" {
					continue
				}
				if !validRoomName.MatchString(room) {
					promptConfirm("Invalid Room Name", "Room name must contain only letters, numbers, dash (-), or underscore (_).")
					continue
				}

				// Passphrase is required — loop until provided or user cancels.
				pass := promptPassphrase(fmt.Sprintf("Enter passphrase for room '%s'.\nAll devices must use the same passphrase.\n\nLeave blank to cancel:", room))
				if pass == "" {
					continue
				}

				if err := relay.SetPassphrase(room, pass); err != nil {
					promptConfirm("Error", fmt.Sprintf("Failed to save passphrase: %v", err))
					continue
				}

				cfg.Relay.Rooms = append(cfg.Relay.Rooms, config.Room{Name: room, Enabled: true})
				if err := config.Save(cfg); err != nil {
					promptConfirm("Error", fmt.Sprintf("Failed to save config: %v", err))
				} else {
					promptConfirm("Room Added", fmt.Sprintf("Added room '%s' with encryption. Restart Paperclip to connect.", room))
				}
			case idx := <-roomRemoveCh:
				if idx >= 0 && idx < len(cfg.Relay.Rooms) {
					name := cfg.Relay.Rooms[idx].Name
					if promptConfirm("Remove Room", fmt.Sprintf("Remove room '%s'?", name)) {
						cfg.Relay.Rooms = append(cfg.Relay.Rooms[:idx], cfg.Relay.Rooms[idx+1:]...)
						if err := config.Save(cfg); err != nil {
							promptConfirm("Error", fmt.Sprintf("Failed to save config: %v", err))
						}
						relay.DeletePassphrase(name)
						promptConfirm("Room Removed", fmt.Sprintf("Removed '%s'. Restart Paperclip for the change to take effect.", name))
					}
				}
			case <-mSetKey.ClickedCh:
				apiKey := promptInput("Ably API Key", "Enter your Ably API key:", "")
				if apiKey != "" {
					if err := relay.SetAPIKey(apiKey); err != nil {
						promptConfirm("Error", fmt.Sprintf("Failed to save API key: %v", err))
					} else {
						promptConfirm("API Key Updated", "Restart Paperclip for the change to take effect.")
					}
				}
			case <-mLogin.ClickedCh:
				if mLogin.Checked() {
					mLogin.Uncheck()
					if err := uninstallLaunchAgent(); err != nil {
						promptConfirm("Warning", fmt.Sprintf("Failed to disable launch agent: %v", err))
					}
				} else {
					mLogin.Check()
					installLaunchAgent(cfg)
				}
			case <-mQuit.ClickedCh:
				if onQuit != nil {
					onQuit()
				}
				systray.Quit()
				return
			}
		}
	}()
}

// promptPassphrase prompts for a passphrase, re-prompting until the user
// enters one of at least 8 characters or explicitly cancels.
func promptPassphrase(message string) string {
	for {
		pass := promptInput("Room Passphrase (Required)", message, "")
		if pass == "" {
			if promptConfirm("Cancel", "No passphrase entered. Cancel?") {
				return ""
			}
			continue
		}
		if len(pass) < 8 {
			promptConfirm("Passphrase Too Short", "Passphrase must be at least 8 characters.")
			continue
		}
		return pass
	}
}

func formatRoomStatus(s relay.RoomStatus) string {
	status := "●"
	if !s.Connected {
		status = "○"
	}
	label := fmt.Sprintf("  %s %s", status, s.Name)
	if s.Encrypted {
		label += " 🔒"
	}
	return label
}
