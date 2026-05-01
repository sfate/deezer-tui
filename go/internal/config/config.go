package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Theme string

const (
	ThemeAetheria Theme = "Aetheria"
	ThemeGruvbox  Theme = "Gruvbox"

	// Deprecated names kept so existing config files migrate cleanly.
	ThemeSpotifyDark Theme = "SpotifyDark"
	ThemeNcmpcppBlue Theme = "NcmpcppBlue"
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
	Theme               Theme        `json:"theme"`
	CrossfadeEnabled    bool         `json:"crossfade_enabled"`
	CrossfadeDurationMS uint64       `json:"crossfade_duration_ms"`
	DefaultQuality      AudioQuality `json:"default_quality"`
	ARL                 string       `json:"arl"`
}

func Default() Config {
	return Config{
		Theme:               ThemeAetheria,
		CrossfadeEnabled:    false,
		CrossfadeDurationMS: 0,
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
	cfg.Theme = NormalizeTheme(cfg.Theme)
	return cfg
}

func NormalizeTheme(theme Theme) Theme {
	switch theme {
	case ThemeGruvbox:
		return ThemeGruvbox
	case ThemeAetheria, ThemeSpotifyDark, ThemeNcmpcppBlue, "":
		return ThemeAetheria
	default:
		return ThemeAetheria
	}
}

func Save(cfg Config) error {
	cfg.Theme = NormalizeTheme(cfg.Theme)
	path, err := ConfigFilePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
