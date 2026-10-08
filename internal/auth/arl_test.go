package auth

import (
	"errors"
	"testing"
)

func TestNormalizeARLAcceptsRawToken(t *testing.T) {
	got, err := NormalizeARL("  token-value  ")
	if err != nil {
		t.Fatalf("normalize raw token: %v", err)
	}
	if got != "token-value" {
		t.Fatalf("expected trimmed token, got %q", got)
	}
}

func TestNormalizeARLExtractsCookie(t *testing.T) {
	got, err := NormalizeARL("sid=session; arl=token-value; theme=dark")
	if err != nil {
		t.Fatalf("normalize cookie: %v", err)
	}
	if got != "token-value" {
		t.Fatalf("expected arl cookie value, got %q", got)
	}
}

func TestNormalizeARLExtractsCookieHeader(t *testing.T) {
	got, err := NormalizeARL("Cookie: sid=session; arl=token-value")
	if err != nil {
		t.Fatalf("normalize cookie header: %v", err)
	}
	if got != "token-value" {
		t.Fatalf("expected arl cookie value, got %q", got)
	}
}

func TestNormalizeARLRejectsEmptyInput(t *testing.T) {
	if _, err := NormalizeARL("  "); !errors.Is(err, ErrARLMissing) {
		t.Fatalf("expected ErrARLMissing, got %v", err)
	}
}

func TestMaskARL(t *testing.T) {
	if got := MaskARL("abcd1234wxyz"); got != "abcd****wxyz" {
		t.Fatalf("unexpected masked token: %q", got)
	}
	if got := MaskARL("short"); got != "********" {
		t.Fatalf("unexpected short masked token: %q", got)
	}
}
