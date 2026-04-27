package tui

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"deezer-tui-go/internal/app"
	"deezer-tui-go/internal/config"
	"deezer-tui-go/internal/deezer"
	"deezer-tui-go/internal/player"
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

type fakePlaybackRuntime struct {
	started []string
	session *fakePlaybackSession
}

func (f *fakePlaybackRuntime) Start(trackID string, _ deezer.AudioQuality, _ player.EventHandler) (PlaybackSession, error) {
	f.started = append(f.started, trackID)
	f.session = &fakePlaybackSession{}
	return f.session, nil
}

type fakePlaybackSession struct {
	paused  bool
	resumed bool
	stopped bool
	waitErr error
	volume  float32
}

func (f *fakePlaybackSession) Pause()              { f.paused = true }
func (f *fakePlaybackSession) Resume()             { f.resumed = true }
func (f *fakePlaybackSession) Stop()               { f.stopped = true }
func (f *fakePlaybackSession) Wait() error         { return f.waitErr }
func (f *fakePlaybackSession) SetVolume(v float32) { f.volume = v }

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
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), loader, runtime)
	model.width = 120
	model.height = 40
	model.app.NavState.Select(intPtr(1))
	model.app.ActivePanel = app.ActivePanelNavigation

	nextModel, cmd := model.Update(tea.KeyPressMsg(tea.Key{Text: "enter"}))
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected flow load command")
	}

	nextModel, playbackCmd := updated.Update(cmd())
	updated = nextModel.(Model)
	if playbackCmd == nil {
		t.Fatal("expected playback start command")
	}

	nextModel, _ = updated.Update(playbackCmd())
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
	if len(runtime.started) != 1 || runtime.started[0] != "201" {
		t.Fatalf("unexpected playback start %#v", runtime.started)
	}
}

func TestTogglePlayPauseControlsSession(t *testing.T) {
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, &fakePlaybackRuntime{})
	session := &fakePlaybackSession{}
	model.session = session
	model.app.IsPlaying = true

	model.togglePlayPause()
	if !session.paused || model.app.IsPlaying {
		t.Fatal("expected pause to be forwarded")
	}

	model.togglePlayPause()
	if !session.resumed || !model.app.IsPlaying {
		t.Fatal("expected resume to be forwarded")
	}
}

func TestPlaybackFinishedAdvancesQueue(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(0)
	model.currentPlayID = 1

	nextModel, cmd := model.Update(playbackFinishedMsg{playID: 1, err: nil})
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected next track command")
	}
	if updated.app.QueueIndex == nil || *updated.app.QueueIndex != 1 {
		t.Fatal("expected queue index to advance")
	}
}

func TestPlaybackErrorStopsPlayback(t *testing.T) {
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, &fakePlaybackRuntime{})
	model.currentPlayID = 1
	model.app.IsPlaying = true

	nextModel, _ := model.Update(playbackFinishedMsg{playID: 1, err: errors.New("boom")})
	updated := nextModel.(Model)
	if updated.app.IsPlaying {
		t.Fatal("expected playback error to stop playback")
	}
}

func TestCanceledPlaybackCompletionIsIgnored(t *testing.T) {
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, &fakePlaybackRuntime{})
	model.currentPlayID = 1
	model.app.IsPlaying = true

	nextModel, cmd := model.Update(playbackFinishedMsg{playID: 1, err: context.Canceled})
	updated := nextModel.(Model)
	if cmd != nil {
		t.Fatal("did not expect follow-up command")
	}
	if !updated.app.IsPlaying {
		t.Fatal("expected canceled completion to leave playback state unchanged")
	}
}
