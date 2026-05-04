//go:build darwin

package tui

import (
	"context"
	"testing"

	"deezer-tui/internal/deezer"
)

func TestDarwinPrebufferTakeKeepsCachedTrackReusable(t *testing.T) {
	store := newDarwinPrebufferStore()
	key := darwinPrebufferKey("1", deezer.AudioQuality320)
	store.current[key] = &darwinPreparedTrack{
		TrackID: "1",
		Quality: deezer.AudioQuality320,
		Meta:    deezer.TrackMetadata{ID: "1", Title: "One", Artist: "A"},
		File:    "/tmp/deezer-tui-test.mp3",
	}
	store.order = []string{key}

	prepared, err := store.TakeOrPrepare(context.Background(), nil, "1", deezer.AudioQuality320, nil)
	if err != nil {
		t.Fatalf("take cached track: %v", err)
	}
	if !prepared.Cached {
		t.Fatal("expected cached prepared track")
	}
	if _, ok := store.current[key]; !ok {
		t.Fatal("expected cached track to remain reusable after take")
	}
}

func TestDarwinPrebufferEmptyWindowKeepsCachedTracks(t *testing.T) {
	store := newDarwinPrebufferStore()
	key := darwinPrebufferKey("1", deezer.AudioQuality320)
	store.current[key] = &darwinPreparedTrack{
		TrackID: "1",
		Quality: deezer.AudioQuality320,
		Meta:    deezer.TrackMetadata{ID: "1", Title: "One", Artist: "A"},
		File:    "/tmp/deezer-tui-test.mp3",
		Cached:  true,
	}
	store.order = []string{key}

	store.Prebuffer(context.Background(), nil, nil, deezer.AudioQuality320, nil)
	if _, ok := store.current[key]; !ok {
		t.Fatal("expected cached track to survive empty prebuffer window")
	}
}
