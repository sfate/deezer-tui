package colorscheme

import "testing"

func TestNormalizeMigratesDeprecatedNamesToAetheria(t *testing.T) {
	for _, name := range []Name{"", SpotifyDark, NcmpcppBlue, "unknown"} {
		if got := Normalize(name); got != Aetheria {
			t.Fatalf("expected %q to normalize to %q, got %q", name, Aetheria, got)
		}
	}
}

func TestLookupReturnsDeclaredPalette(t *testing.T) {
	scheme := Lookup(Gruvbox)

	if scheme.Name != Gruvbox {
		t.Fatalf("expected gruvbox scheme, got %q", scheme.Name)
	}
	if scheme.Palette.Background != "#282828" {
		t.Fatalf("expected gruvbox background, got %s", scheme.Palette.Background)
	}
}

func TestNextCyclesSchemes(t *testing.T) {
	if got := Next(Aetheria, 1); got != Gruvbox {
		t.Fatalf("expected next aetheria theme to be gruvbox, got %q", got)
	}
	if got := Next(Aetheria, -1); got != Gruvbox {
		t.Fatalf("expected previous aetheria theme to wrap to gruvbox, got %q", got)
	}
	if got := Next(Gruvbox, 1); got != Aetheria {
		t.Fatalf("expected next gruvbox theme to wrap to aetheria, got %q", got)
	}
}

func TestAllReturnsCopy(t *testing.T) {
	all := All()
	all[0].Name = "mutated"

	if got := All()[0].Name; got != Aetheria {
		t.Fatalf("expected scheme registry to be immutable through All, got %q", got)
	}
}
