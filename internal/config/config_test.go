package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveWritesConfigOwnerOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := Save(Config{ARL: "secret"}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	info, err := os.Stat(filepath.Join(home, ".deezer-tui-config.json"))
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected config mode 0600, got %04o", got)
	}
}

func TestNormalizeDisplayModeFallsBackToOffForUnknownMode(t *testing.T) {
	got := NormalizeDisplayMode(DisplayMode("spectrum"), true)
	if got != DisplayModeOff {
		t.Fatalf("expected unknown display mode to fall back to off, got %q", got)
	}
}
