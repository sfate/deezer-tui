package colorscheme

import "testing"

func TestNormalizeFallsBackToAetheria(t *testing.T) {
	for _, name := range []Name{"", "unknown"} {
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

	scheme = Lookup(Winamp)
	if scheme.Name != Winamp {
		t.Fatalf("expected winamp scheme, got %q", scheme.Name)
	}
	if scheme.Palette.Green != "#27ff57" {
		t.Fatalf("expected winamp display green, got %s", scheme.Palette.Green)
	}
}

func TestNextCyclesSchemes(t *testing.T) {
	if got := Next(Aetheria, 1); got != Gruvbox {
		t.Fatalf("expected next aetheria theme to be gruvbox, got %q", got)
	}
	if got := Next(Gruvbox, 1); got != Winamp {
		t.Fatalf("expected next gruvbox theme to be winamp, got %q", got)
	}
	if got := Next(Winamp, 1); got != Aetheria {
		t.Fatalf("expected next winamp theme to wrap to aetheria, got %q", got)
	}
	if got := Next(Aetheria, -1); got != Winamp {
		t.Fatalf("expected previous aetheria theme to wrap to winamp, got %q", got)
	}
}

func TestAllReturnsCopy(t *testing.T) {
	all := All()
	all[0].Name = "mutated"

	if got := All()[0].Name; got != Aetheria {
		t.Fatalf("expected scheme registry to be immutable through All, got %q", got)
	}
}
