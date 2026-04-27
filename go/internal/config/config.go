package config

type Theme string

const (
	ThemeSpotifyDark Theme = "spotify_dark"
	ThemeNcmpcppBlue Theme = "ncmpcpp_blue"
)

type AudioQuality uint8

const (
	AudioQuality128 AudioQuality = iota
	AudioQuality320
	AudioQualityFlac
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
		return 0, false
	}
}

type Config struct {
	Theme               Theme
	CrossfadeEnabled    bool
	CrossfadeDurationMS uint64
	DefaultQuality      AudioQuality
	DiscordRPCEnabled   bool
	ARL                 string
}

func Default() Config {
	return Config{
		Theme:               ThemeSpotifyDark,
		CrossfadeEnabled:    false,
		CrossfadeDurationMS: 0,
		DefaultQuality:      AudioQuality320,
		DiscordRPCEnabled:   false,
		ARL:                 "",
	}
}
