package colorscheme

type Name string

const (
	Aetheria Name = "Aetheria"
	Gruvbox  Name = "Gruvbox"

	// Deprecated names kept so existing config files migrate cleanly.
	SpotifyDark Name = "SpotifyDark"
	NcmpcppBlue Name = "NcmpcppBlue"
)

type Palette struct {
	BackgroundHard string
	Background     string
	Border         string
	TextStrong     string
	Text           string
	TextMuted      string
	Yellow         string
	Blue           string
	Aqua           string
	Green          string
	Orange         string
	Purple         string
}

type Scheme struct {
	Name    Name
	Label   string
	Palette Palette
}

var schemes = []Scheme{
	{
		Name:  Aetheria,
		Label: "Aetheria",
		Palette: Palette{
			BackgroundHard: "#100c18",
			Background:     "#15111f",
			Border:         "#3d314a",
			TextStrong:     "#e4d4de",
			Text:           "#c8b3bf",
			TextMuted:      "#8f7383",
			Yellow:         "#f3c969",
			Blue:           "#8f7383",
			Aqua:           "#21c7d9",
			Green:          "#21c7d9",
			Orange:         "#e07a87",
			Purple:         "#b18bb8",
		},
	},
	{
		Name:  Gruvbox,
		Label: "Gruvbox",
		Palette: Palette{
			BackgroundHard: "#1d2021",
			Background:     "#282828",
			Border:         "#665c54",
			TextStrong:     "#fbf1c7",
			Text:           "#ebdbb2",
			TextMuted:      "#a89984",
			Yellow:         "#fabd2f",
			Blue:           "#83a598",
			Aqua:           "#8ec07c",
			Green:          "#b8bb26",
			Orange:         "#fe8019",
			Purple:         "#d3869b",
		},
	},
}

func All() []Scheme {
	out := make([]Scheme, len(schemes))
	copy(out, schemes)
	return out
}

func Normalize(name Name) Name {
	switch name {
	case Gruvbox:
		return Gruvbox
	case Aetheria, SpotifyDark, NcmpcppBlue, "":
		return Aetheria
	default:
		return Aetheria
	}
}

func Lookup(name Name) Scheme {
	name = Normalize(name)
	for _, scheme := range schemes {
		if scheme.Name == name {
			return scheme
		}
	}
	return schemes[0]
}

func Label(name Name) string {
	return Lookup(name).Label
}

func Next(current Name, direction int) Name {
	current = Normalize(current)
	idx := 0
	for i, scheme := range schemes {
		if scheme.Name == current {
			idx = i
			break
		}
	}
	if direction < 0 {
		idx = (idx - 1 + len(schemes)) % len(schemes)
	} else {
		idx = (idx + 1) % len(schemes)
	}
	return schemes[idx].Name
}
