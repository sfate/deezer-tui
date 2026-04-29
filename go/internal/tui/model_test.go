package tui

import (
	"context"
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"
	"unicode/utf8"

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
	search    SearchData
	queries   []string
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

func (f *fakeLoader) Search(_ context.Context, query string) (SearchData, error) {
	f.queries = append(f.queries, query)
	return f.search, nil
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

func TestTruncatePreservesUTF8(t *testing.T) {
	got := truncate("Рор Hits 2026 | N...", 12)
	if got == "" {
		t.Fatal("expected non-empty result")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncate returned invalid utf8: %q", got)
	}
}

func TestViewShowsProgressBar(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.width = 120
	model.height = 40
	model.app.ActivePanel = app.ActivePanelMain
	model.app.CurrentTracks = []app.Track{
		{ID: "1", Title: "Selected Song", Artist: "Chosen Artist"},
	}
	model.app.MainState.Select(intPtr(1))
	model.app.NowPlaying = &app.NowPlaying{
		ID:        "2",
		Title:     "Live Song",
		Artist:    "Current Artist",
		CurrentMS: 45000,
		TotalMS:   180000,
	}
	model.app.IsPlaying = true
	model.artworkANSI = strings.Join([]string{
		"\x1b[38;2;255;0;0m▀▀▀▀▀▀▀▀▀▀▀▀▀▀\x1b[0m",
		"\x1b[38;2;0;255;0m▀▀▀▀▀▀▀▀▀▀▀▀▀▀\x1b[0m",
		"\x1b[38;2;0;0;255m▀▀▀▀▀▀▀▀▀▀▀▀▀▀\x1b[0m",
		"\x1b[38;2;255;255;0m▀▀▀▀▀▀▀▀▀▀▀▀▀▀\x1b[0m",
		"\x1b[38;2;255;0;255m▀▀▀▀▀▀▀▀▀▀▀▀▀▀\x1b[0m",
		"\x1b[38;2;0;255;255m▀▀▀▀▀▀▀▀▀▀▀▀▀▀\x1b[0m",
		"\x1b[38;2;255;128;0m▀▀▀▀▀▀▀▀▀▀▀▀▀▀\x1b[0m",
		"\x1b[38;2;128;0;255m▀▀▀▀▀▀▀▀▀▀▀▀▀▀\x1b[0m",
		"\x1b[38;2;255;0;0m▀▀▀▀▀▀▀▀▀▀▀▀▀▀\x1b[0m",
		"\x1b[38;2;0;255;0m▀▀▀▀▀▀▀▀▀▀▀▀▀▀\x1b[0m",
		"\x1b[38;2;0;0;255m▀▀▀▀▀▀▀▀▀▀▀▀▀▀\x1b[0m",
		"\x1b[38;2;255;255;0m▀▀▀▀▀▀▀▀▀▀▀▀▀▀\x1b[0m",
		"\x1b[38;2;255;0;255m▀▀▀▀▀▀▀▀▀▀▀▀▀▀\x1b[0m",
		"\x1b[38;2;0;255;255m▀▀▀▀▀▀▀▀▀▀▀▀▀▀\x1b[0m",
	}, "\n")

	view := model.View().Content
	if !strings.Contains(view, "[") || !strings.Contains(view, "00:45 / 03:00") {
		t.Fatal("expected progress bar in view")
	}
	if !strings.Contains(view, "Track:") {
		t.Fatal("expected track info in status area")
	}
	if !strings.Contains(view, "State:") {
		t.Fatal("expected state line in status area")
	}
	if !strings.Contains(view, "Quality:") || !strings.Contains(view, "Source:") {
		t.Fatal("expected track metadata lines in status area")
	}
	if !strings.Contains(view, "Live Song") {
		t.Fatal("expected current song title in status area")
	}
	if !strings.Contains(view, "03:00") || !strings.Contains(view, "320 kbps") {
		t.Fatal("expected useful track metadata in info line")
	}
	if !strings.Contains(view, "▀") {
		t.Fatal("expected artwork block in status area")
	}
	if !strings.Contains(view, "space play/pause") {
		t.Fatal("expected spacebar control hint in view")
	}
}

func TestSpacebarStartsSelectedTrack(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.width = 120
	model.height = 40
	model.app.ActivePanel = app.ActivePanelMain
	model.app.CurrentTracks = []app.Track{
		{ID: "1", Title: "Selected Song", Artist: "Chosen Artist"},
	}
	model.app.MainState.Select(intPtr(1))

	nextModel, cmd := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeySpace}))
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected playback command from spacebar")
	}

	nextModel, _ = updated.Update(cmd())
	updated = nextModel.(Model)
	if len(runtime.started) != 1 || runtime.started[0] != "1" {
		t.Fatalf("unexpected playback start %#v", runtime.started)
	}
	if !updated.app.IsPlaying {
		t.Fatal("expected playback state to start")
	}
}

func TestPlaybackStartHonorsPendingPause(t *testing.T) {
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, &fakePlaybackRuntime{})
	session := &fakePlaybackSession{}
	model.nextPlaybackID = 1
	model.pauseRequested = true

	nextModel, cmd := model.Update(playbackStartedMsg{playID: 1, session: session})
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected follow-up playback wait/listen commands")
	}
	if !session.paused {
		t.Fatal("expected session to be paused immediately")
	}
	if updated.app.IsPlaying {
		t.Fatal("expected app to stay paused")
	}
}

func TestDisplayStateUsesStatusMessage(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.app.StatusMessage = "Buffering..."
	if got := model.displayState(); got != "Buffering" {
		t.Fatalf("expected Buffering state, got %q", got)
	}
}

func TestRenderArtworkANSIProducesBlockGrid(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 40), G: uint8(y * 40), B: 120, A: 255})
		}
	}

	art := renderArtworkANSI(img, 4, 4)
	lines := strings.Split(art, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 artwork rows, got %d", len(lines))
	}
	if !strings.Contains(art, "▀") {
		t.Fatal("expected half-block artwork glyphs")
	}
	if !strings.Contains(art, "\x1b[38;2;") || !strings.Contains(art, "\x1b[48;2;") {
		t.Fatal("expected ANSI foreground/background color sequences in artwork")
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

func TestEnterOnSearchPlaylistLoadsPlaylist(t *testing.T) {
	loader := &fakeLoader{
		search: SearchData{
			Playlists: []app.Playlist{{ID: "pl-1", Title: "Focus"}},
		},
		playlist: []app.Track{{ID: "11", Title: "Track", Artist: "Artist"}},
	}
	model := NewWithLoaderAndRuntime(config.Default(), loader, &fakePlaybackRuntime{})
	model.width = 120
	model.height = 40
	model.app.ShowingSearchResult = true
	model.app.SearchCategory = app.SearchCategoryPlaylists
	model.app.SearchPlaylists = loader.search.Playlists
	model.app.ActivePanel = app.ActivePanelMain
	model.app.MainState.Select(intPtr(0))

	nextModel, cmd := model.Update(tea.KeyPressMsg(tea.Key{Text: "enter"}))
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected playlist load command")
	}

	nextModel, _ = updated.Update(cmd())
	updated = nextModel.(Model)
	if updated.app.CurrentPlaylistID == nil || *updated.app.CurrentPlaylistID != "pl-1" {
		t.Fatal("expected selected playlist to load")
	}
	if len(updated.app.CurrentTracks) != 1 {
		t.Fatal("expected playlist tracks to load")
	}
}

func TestEnterOnSearchArtistStartsSearch(t *testing.T) {
	loader := &fakeLoader{
		search: SearchData{
			Tracks: []app.Track{{ID: "99", Title: "Artist Track", Artist: "Aster Lane"}},
		},
	}
	model := NewWithLoaderAndRuntime(config.Default(), loader, &fakePlaybackRuntime{})
	model.width = 120
	model.height = 40
	model.app.ShowingSearchResult = true
	model.app.SearchCategory = app.SearchCategoryArtists
	model.app.SearchArtists = []app.Artist{{ID: "ar-1", Name: "Aster Lane"}}
	model.app.ActivePanel = app.ActivePanelMain
	model.app.MainState.Select(intPtr(0))

	nextModel, cmd := model.Update(tea.KeyPressMsg(tea.Key{Text: "enter"}))
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected artist search command")
	}
	if updated.app.SearchQuery != "Aster Lane" {
		t.Fatal("expected artist name to become the active search query")
	}

	nextModel, _ = updated.Update(cmd())
	updated = nextModel.(Model)
	if len(loader.queries) == 0 || loader.queries[0] != "Aster Lane" {
		t.Fatal("expected loader search to receive the artist name")
	}
	if len(updated.app.CurrentTracks) != 1 {
		t.Fatal("expected artist follow-up search results to load")
	}
}
