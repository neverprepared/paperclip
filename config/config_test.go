package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.PollMs != 500 {
		t.Errorf("expected default PollMs=500, got %d", cfg.PollMs)
	}
	if cfg.Verbose {
		t.Error("expected Verbose=false by default")
	}
	if len(cfg.Relay.Rooms) != 0 {
		t.Errorf("expected no rooms by default, got %d", len(cfg.Relay.Rooms))
	}
}

func TestEnabledRooms(t *testing.T) {
	cfg := &Config{
		Relay: RelayConfig{
			Rooms: []Room{
				{Name: "alpha", Enabled: true},
				{Name: "beta", Enabled: false},
				{Name: "gamma", Enabled: true},
			},
		},
	}

	enabled := cfg.Relay.EnabledRooms()
	if len(enabled) != 2 {
		t.Fatalf("expected 2 enabled rooms, got %d", len(enabled))
	}
	if enabled[0].Name != "alpha" {
		t.Errorf("expected first enabled room to be 'alpha', got %q", enabled[0].Name)
	}
	if enabled[1].Name != "gamma" {
		t.Errorf("expected second enabled room to be 'gamma', got %q", enabled[1].Name)
	}
}

func TestEnabledRoomsNone(t *testing.T) {
	cfg := &Config{
		Relay: RelayConfig{
			Rooms: []Room{
				{Name: "alpha", Enabled: false},
			},
		},
	}
	if len(cfg.Relay.EnabledRooms()) != 0 {
		t.Error("expected no enabled rooms")
	}
}

func TestEnabledRoomsEmpty(t *testing.T) {
	cfg := DefaultConfig()
	if rooms := cfg.Relay.EnabledRooms(); len(rooms) != 0 {
		t.Errorf("expected empty enabled rooms, got %d", len(rooms))
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	original := &Config{
		PollMs:  250,
		Verbose: true,
		Relay: RelayConfig{
			Rooms: []Room{
				{Name: "my-room", Enabled: true},
				{Name: "other", Enabled: false},
			},
		},
	}

	if err := SaveTo(path, original); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	loaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	if loaded.PollMs != original.PollMs {
		t.Errorf("PollMs: got %d, want %d", loaded.PollMs, original.PollMs)
	}
	if loaded.Verbose != original.Verbose {
		t.Errorf("Verbose: got %v, want %v", loaded.Verbose, original.Verbose)
	}
	if len(loaded.Relay.Rooms) != len(original.Relay.Rooms) {
		t.Fatalf("Rooms len: got %d, want %d", len(loaded.Relay.Rooms), len(original.Relay.Rooms))
	}
	for i, r := range original.Relay.Rooms {
		if loaded.Relay.Rooms[i].Name != r.Name {
			t.Errorf("Room[%d].Name: got %q, want %q", i, loaded.Relay.Rooms[i].Name, r.Name)
		}
		if loaded.Relay.Rooms[i].Enabled != r.Enabled {
			t.Errorf("Room[%d].Enabled: got %v, want %v", i, loaded.Relay.Rooms[i].Enabled, r.Enabled)
		}
	}
}

func TestLoadFromMissingFileReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if cfg.PollMs != 500 {
		t.Errorf("expected default PollMs=500, got %d", cfg.PollMs)
	}
}

func TestLoadFromCorruptJSONReturnsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := os.WriteFile(path, []byte("{not valid json"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err == nil {
		t.Error("expected error for corrupt JSON, got nil")
	}
	// Should still return usable defaults despite the error.
	if cfg == nil {
		t.Fatal("expected non-nil config even on error")
	}
	if cfg.PollMs != 500 {
		t.Errorf("expected default PollMs=500 on corrupt file, got %d", cfg.PollMs)
	}
}

func TestSaveToFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := SaveTo(path, DefaultConfig()); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	mode := info.Mode().Perm()
	if mode != 0600 {
		t.Errorf("expected file mode 0600, got %04o", mode)
	}
}

func TestLoadFromPartialJSONUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Only override poll_ms; Verbose should default to false.
	if err := os.WriteFile(path, []byte(`{"poll_ms": 100}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.PollMs != 100 {
		t.Errorf("expected PollMs=100, got %d", cfg.PollMs)
	}
	if cfg.Verbose {
		t.Error("expected Verbose=false (default), got true")
	}
}
