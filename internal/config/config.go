package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"deezer-tui/internal/colorscheme"
)

type AudioQuality string

const (
	AudioQuality128  AudioQuality = "Kbps128"
	AudioQuality320  AudioQuality = "Kbps320"
	AudioQualityFlac AudioQuality = "Flac"
)

func (q AudioQuality) FormatCode() uint8 {
	switch q {
	case AudioQuality128:
		return 1
	case AudioQuality320:
		return 3
	case AudioQualityFlac:
		return 9
	default:
		return 0
	}
}

func AudioQualityFromFormatCode(code uint8) (AudioQuality, bool) {
	switch code {
	case 1:
		return AudioQuality128, true
	case 3:
		return AudioQuality320, true
	case 9:
		return AudioQualityFlac, true
	default:
		return "", false
	}
}

type Config struct {
	Theme               colorscheme.Name `json:"theme"`
	CrossfadeEnabled    bool             `json:"crossfade_enabled"`
	CrossfadeDurationMS uint64           `json:"crossfade_duration_ms"`
	DisplayMode         DisplayMode      `json:"display_mode"`
	DisplayEnabled      bool             `json:"display_enabled,omitempty"`
	DefaultQuality      AudioQuality     `json:"default_quality"`
	ARL                 string           `json:"arl"`
}

type DisplayMode string

const (
	DisplayModeOff       DisplayMode = "off"
	DisplayModeEqualizer DisplayMode = "equalizer"
)

func Default() Config {
	return Config{
		Theme:               colorscheme.Aetheria,
		CrossfadeEnabled:    false,
		CrossfadeDurationMS: 0,
		DisplayMode:         DisplayModeEqualizer,
		DisplayEnabled:      true,
		DefaultQuality:      AudioQuality320,
		ARL:                 "",
	}
}

func ConfigFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("could not resolve the user's home directory")
	}
	return filepath.Join(home, ".deezer-tui-config.json"), nil
}

func Load() Config {
	path, err := ConfigFilePath()
	if err != nil {
		return Default()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Default()
	}

	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default()
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(data, &raw)
	cfg.Theme = colorscheme.Normalize(cfg.Theme)
	if _, ok := raw["display_mode"]; !ok {
		_, hasLegacyDisplayEnabled := raw["display_enabled"]
		cfg.DisplayMode = NormalizeDisplayMode("", !hasLegacyDisplayEnabled || cfg.DisplayEnabled)
	} else {
		cfg.DisplayMode = NormalizeDisplayMode(cfg.DisplayMode, cfg.DisplayEnabled)
	}
	cfg.DisplayEnabled = cfg.DisplayMode != DisplayModeOff
	return cfg
}

func Save(cfg Config) error {
	cfg.Theme = colorscheme.Normalize(cfg.Theme)
	cfg.DisplayMode = NormalizeDisplayMode(cfg.DisplayMode, cfg.DisplayEnabled)
	cfg.DisplayEnabled = cfg.DisplayMode != DisplayModeOff
	path, err := ConfigFilePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func NormalizeDisplayMode(mode DisplayMode, legacyEnabled bool) DisplayMode {
	switch mode {
	case DisplayModeOff, DisplayModeEqualizer:
		return mode
	case "":
		if !legacyEnabled {
			return DisplayModeOff
		}
		return DisplayModeEqualizer
	default:
		return DisplayModeEqualizer
	}
}
