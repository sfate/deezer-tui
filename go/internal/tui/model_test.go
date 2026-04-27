package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"deezer-tui-go/internal/app"
	"deezer-tui-go/internal/config"
)

type fakeLoader struct {
	bootstrap BootstrapData
	home      []app.Track
	flow      []app.Track
	explore   []app.Track
	favorites []app.Track
	playlist  []app.Track
}

func (f *fakeLoader) Bootstrap(context.Context) (BootstrapData, error) {
	return f.bootstrap, nil
}

func (f *fakeLoader) LoadHome(context.Context) ([]app.Track, error) {
	return append([]app.Track(nil), f.home...), nil
}

func (f *fakeLoader) LoadFlow(context.Context, int) ([]app.Track, error) {
	return append([]app.Track(nil), f.flow...), nil
}

func (f *fakeLoader) LoadExplore(context.Context) ([]app.Track, error) {
	return append([]app.Track(nil), f.explore...), nil
}

func (f *fakeLoader) LoadFavorites(context.Context) ([]app.Track, error) {
	return append([]app.Track(nil), f.favorites...), nil
}

func (f *fakeLoader) LoadPlaylist(context.Context, string) ([]app.Track, error) {
	return append([]app.Track(nil), f.playlist...), nil
}

func TestViewUsesAltScreen(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.width = 120
	model.height = 40

	view := model.View()
	if !view.AltScreen {
		t.Fatal("expected alt screen to be enabled")
	}
	if view.WindowTitle != "deezer-tui-go" {
		t.Fatalf("unexpected window title %q", view.WindowTitle)
	}
}

func TestInitBootstrapsPlaylistsAndLoadsHome(t *testing.T) {
	loader := &fakeLoader{
		bootstrap: BootstrapData{
			Playlists: []app.Playlist{{ID: "1", Title: "Daily"}},
		},
		home: []app.Track{{ID: "11", Title: "Track", Artist: "Artist"}},
	}
	model := NewWithLoader(config.Default(), loader)
	model.width = 120
	model.height = 40

	initCmd := model.Init()
	msg := initCmd()
	nextModel, cmd := model.Update(msg)
	updated := nextModel.(Model)

	if len(updated.app.Playlists) != 1 {
		t.Fatalf("expected playlists to load, got %d", len(updated.app.Playlists))
	}
	if cmd == nil {
		t.Fatal("expected follow-up home load command")
	}

	msg = cmd()
	nextModel, _ = updated.Update(msg)
	updated = nextModel.(Model)
	if updated.app.CurrentPlaylistID == nil || *updated.app.CurrentPlaylistID != "__home__" {
		t.Fatal("expected home playlist to be selected")
	}
	if len(updated.app.CurrentTracks) != 1 {
		t.Fatalf("expected home tracks to load, got %d", len(updated.app.CurrentTracks))
	}
}

func TestEnterOnFlowLoadsQueueAndMarksFlow(t *testing.T) {
	loader := &fakeLoader{
		flow: []app.Track{
			{ID: "201", Title: "Current Drift", Artist: "Velvet Echo"},
			{ID: "202", Title: "Night Transit", Artist: "Aster Lane"},
		},
	}
	model := NewWithLoader(config.Default(), loader)
	model.width = 120
	model.height = 40
	model.app.NavState.Select(intPtr(1))
	model.app.ActivePanel = app.ActivePanelNavigation

	nextModel, cmd := model.Update(tea.KeyPressMsg(tea.Key{Text: "enter"}))
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected flow load command")
	}

	nextModel, _ = updated.Update(cmd())
	updated = nextModel.(Model)

	if updated.app.CurrentPlaylistID == nil || *updated.app.CurrentPlaylistID != "__flow__" {
		t.Fatal("expected flow playlist to be selected")
	}
	if !updated.app.IsFlowQueue {
		t.Fatal("expected flow queue to be active")
	}
	if !updated.app.IsPlaying {
		t.Fatal("expected flow selection to start playback")
	}
	if updated.app.QueueIndex == nil || *updated.app.QueueIndex != 0 {
		t.Fatal("expected queue to start from first track")
	}
	if len(updated.app.QueueTracks) != len(loader.flow) {
		t.Fatalf("unexpected queue length %d", len(updated.app.QueueTracks))
	}
}
