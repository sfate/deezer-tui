package auth

import "testing"

func TestARLFromCookies(t *testing.T) {
	got := arlFromCookies([]cdpCookie{
		{Name: "sid", Value: "ignored", Domain: ".deezer.com"},
		{Name: "arl", Value: "token-value", Domain: ".deezer.com"},
	})
	if got != "token-value" {
		t.Fatalf("expected arl cookie, got %q", got)
	}
}

func TestARLFromCookiesIgnoresOtherDomains(t *testing.T) {
	got := arlFromCookies([]cdpCookie{
		{Name: "arl", Value: "token-value", Domain: ".example.com"},
	})
	if got != "" {
		t.Fatalf("expected non-deezer cookie to be ignored, got %q", got)
	}
}
