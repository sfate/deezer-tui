package tui

import (
	"context"
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"
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
	started     []string
	qualities   []deezer.AudioQuality
	prebuffered []string
	session     *fakePlaybackSession
}

func (f *fakePlaybackRuntime) Start(trackID string, quality deezer.AudioQuality, _ player.EventHandler) (PlaybackSession, error) {
	f.started = append(f.started, trackID)
	f.qualities = append(f.qualities, quality)
	f.session = &fakePlaybackSession{}
	return f.session, nil
}

func (f *fakePlaybackRuntime) Prebuffer(trackID string, _ deezer.AudioQuality) {
	f.prebuffered = append(f.prebuffered, trackID)
}

type fakePlaybackSession struct {
	paused  bool
	resumed bool
	stopped bool
	waitErr error
	volume  float32
	faded   chan time.Duration
}

func (f *fakePlaybackSession) Pause()              { f.paused = true }
func (f *fakePlaybackSession) Resume()             { f.resumed = true }
func (f *fakePlaybackSession) Stop()               { f.stopped = true }
func (f *fakePlaybackSession) Wait() error         { return f.waitErr }
func (f *fakePlaybackSession) SetVolume(v float32) { f.volume = v }
func (f *fakePlaybackSession) FadeOutStop(d time.Duration) {
	if f.faded != nil {
		f.faded <- d
	}
}

func TestViewUsesAltScreen(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.width = 120
	model.height = 40
	model.ready = true

	view := model.View()
	if !view.AltScreen {
		t.Fatal("expected alt screen to be enabled")
	}
	if view.WindowTitle != "deezer-tui-go" {
		t.Fatalf("unexpected window title %q", view.WindowTitle)
	}
}

func TestViewShowsLoadingLogoBeforeInitialCollectionLoad(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.width = 120
	model.height = 40
	model.ready = false

	view := model.View().Content
	if !strings.Contains(view, "██████") {
		t.Fatal("expected loading logo in startup view")
	}
}

func TestInitialLoadFailureLeavesLoadingScreen(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.ready = false

	nextModel, _ := model.Update(loadFailedMsg{message: "Bootstrap error: denied"})
	updated := nextModel.(Model)
	if !updated.ready {
		t.Fatal("expected load failure to make the app ready")
	}
	if updated.app.StatusMessage != "Bootstrap error: denied" {
		t.Fatalf("expected failure status to be shown, got %q", updated.app.StatusMessage)
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
	model.ready = true
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

func TestMiddleColumnsUseEqualWidths(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.width = 120
	model.height = 40
	model.ready = true

	sidebarWidth := min(28, max(22, model.width/5))
	middleAvailable := max(72, model.width-sidebarWidth-4)
	queueWidth := middleAvailable / 2
	mainWidth := middleAvailable - queueWidth

	queuePanel := model.renderQueue(queueWidth, 10)
	mainPanel := model.renderMain(mainWidth, 10)
	if textWidth(strings.Split(queuePanel, "\n")[0]) != textWidth(strings.Split(mainPanel, "\n")[0]) {
		t.Fatalf("expected queue and main columns to have equal widths, got %d and %d", queueWidth, mainWidth)
	}
}

func TestQueueRowsUseQueuePanelWidth(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.app.ActivePanel = app.ActivePanelQueue
	model.app.QueueTracks = []app.Track{{
		ID:     "1",
		Title:  "Very Long Queue Track Title That Should Use Full Queue Width",
		Artist: "Wide Artist",
	}}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(0)
	model.app.QueueState.Select(intPtr(0))

	view := model.renderQueue(80, 20)
	if !strings.Contains(view, "Very Long Queue Track Title") {
		t.Fatal("expected queue row to use the wider queue panel width")
	}
}

func TestSettingsViewShowsEditableSettingsWithoutDiscord(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.app.ViewingSettings = true
	model.app.ActivePanel = app.ActivePanelMain
	model.app.SettingsState.Select(intPtr(0))

	view := model.renderMain(80, 12)
	for _, want := range []string{"Theme:", "Aetheria", "Volume:", "Quality:", "Crossfade:", "Duration:"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected settings view to contain %q", want)
		}
	}
	if strings.Contains(view, "Discord") {
		t.Fatal("did not expect Discord setting to be rendered")
	}
}

func TestSettingsQualityPersistsAndAffectsPlayback(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultQuality = config.AudioQuality320
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(cfg, &fakeLoader{}, runtime)
	var saved []config.Config
	model.saveConfig = func(cfg config.Config) error {
		saved = append(saved, cfg)
		return nil
	}
	model.app.ViewingSettings = true
	model.app.ActivePanel = app.ActivePanelMain
	model.app.SettingsState.Select(intPtr(2))

	nextModel, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "enter"}))
	updated := nextModel.(Model)
	if updated.app.Config.DefaultQuality != config.AudioQualityFlac {
		t.Fatalf("expected quality to cycle to FLAC, got %s", updated.app.Config.DefaultQuality)
	}
	if len(saved) != 1 || saved[0].DefaultQuality != config.AudioQualityFlac {
		t.Fatalf("expected FLAC quality to be persisted, got %#v", saved)
	}

	cmd := updated.startTrackPlayback("track-1")
	if cmd == nil {
		t.Fatal("expected playback command")
	}
	nextModel, _ = updated.Update(cmd())
	_ = nextModel.(Model)
	if len(runtime.qualities) != 1 || runtime.qualities[0] != deezer.AudioQualityFlac {
		t.Fatalf("expected playback to use FLAC quality, got %#v", runtime.qualities)
	}
}

func TestSettingsThemePersistsAndAppliesImmediately(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	var saved []config.Config
	model.saveConfig = func(cfg config.Config) error {
		saved = append(saved, cfg)
		return nil
	}
	model.app.ViewingSettings = true
	model.app.ActivePanel = app.ActivePanelMain
	model.app.SettingsState.Select(intPtr(0))

	nextModel, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "enter"}))
	updated := nextModel.(Model)
	if updated.app.Config.Theme != config.ThemeGruvbox {
		t.Fatalf("expected theme to cycle to gruvbox, got %s", updated.app.Config.Theme)
	}
	if len(saved) != 1 || saved[0].Theme != config.ThemeGruvbox {
		t.Fatalf("expected gruvbox theme to be persisted, got %#v", saved)
	}
	if gruvboxBg0 != "#282828" {
		t.Fatalf("expected gruvbox palette to apply, got bg %s", gruvboxBg0)
	}
}

func TestEscFocusesLibraryAndKeepsCollection(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.app.ViewingSettings = true
	model.app.ActivePanel = app.ActivePanelMain
	model.app.CurrentPlaylistID = stringPtr("__home__")
	model.app.CurrentTracks = []app.Track{{ID: "1", Title: "One", Artist: "A"}}

	nextModel, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "esc"}))
	updated := nextModel.(Model)
	if updated.app.ViewingSettings {
		t.Fatal("expected settings to close")
	}
	if updated.app.ActivePanel != app.ActivePanelNavigation {
		t.Fatalf("expected focus to move to library, got %v", updated.app.ActivePanel)
	}
	if updated.app.CurrentPlaylistID == nil || *updated.app.CurrentPlaylistID != "__home__" || len(updated.app.CurrentTracks) != 1 {
		t.Fatal("expected current collection to remain loaded")
	}
}

func TestEscWhileSearchingFocusesLibrary(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.app.IsSearching = true
	model.app.ActivePanel = app.ActivePanelSearch
	model.app.SearchQuery = "artist"

	nextModel, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "esc"}))
	updated := nextModel.(Model)
	if updated.app.IsSearching {
		t.Fatal("expected search input to close")
	}
	if updated.app.ActivePanel != app.ActivePanelNavigation {
		t.Fatalf("expected focus to move to library, got %v", updated.app.ActivePanel)
	}
}

func TestSettingsVolumeTakesEffectOnActiveSession(t *testing.T) {
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, &fakePlaybackRuntime{})
	session := &fakePlaybackSession{}
	model.session = session
	model.app.Volume = 50
	model.app.ViewingSettings = true
	model.app.ActivePanel = app.ActivePanelMain
	model.app.SettingsState.Select(intPtr(1))

	nextModel, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "l"}))
	updated := nextModel.(Model)
	if updated.app.Volume != 55 {
		t.Fatalf("expected volume to increase, got %d", updated.app.Volume)
	}
	if session.volume != 0.55 {
		t.Fatalf("expected active session volume to update, got %f", session.volume)
	}
}

func TestCrossfadeSettingUsesFadeStopWhenReplacingSession(t *testing.T) {
	session := &fakePlaybackSession{faded: make(chan time.Duration, 1)}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, &fakePlaybackRuntime{})
	model.session = session
	model.app.Config.CrossfadeEnabled = true
	model.app.Config.CrossfadeDurationMS = 3000

	model.stopCurrentSession()

	select {
	case got := <-session.faded:
		if got != 3*time.Second {
			t.Fatalf("expected 3s fade, got %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("expected fade stop to be used")
	}
	if session.stopped {
		t.Fatal("did not expect immediate stop when fade stop is available")
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

func TestBufferingProgressShowsPercentage(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.currentPlayID = 7

	nextModel, _ := model.Update(bufferingProgressMsg{playID: 7, percent: 42})
	updated := nextModel.(Model)
	if got := updated.displayState(); got != "Buffering 42%" {
		t.Fatalf("expected buffering percentage, got %q", got)
	}

	nextModel, _ = updated.Update(playbackTrackChangedMsg{
		playID:  7,
		meta:    deezer.TrackMetadata{ID: "1", Title: "Song", Artist: "Artist"},
		quality: deezer.AudioQuality320,
	})
	updated = nextModel.(Model)
	if updated.bufferingPercent != nil {
		t.Fatal("expected buffering percentage to clear after track metadata arrives")
	}
}

func TestBufferingProgressKeepsListeningForTrackChanged(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.currentPlayID = 7
	model.playbackEvents <- playbackTrackChangedMsg{
		playID:  7,
		meta:    deezer.TrackMetadata{ID: "1", Title: "Loaded Song", Artist: "Artist"},
		quality: deezer.AudioQuality320,
	}

	nextModel, cmd := model.Update(bufferingProgressMsg{playID: 7, percent: 42})
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected buffering progress to keep listening for playback events")
	}

	nextModel, _ = updated.Update(cmd())
	updated = nextModel.(Model)
	if updated.app.NowPlaying == nil || updated.app.NowPlaying.Title != "Loaded Song" {
		t.Fatal("expected track changed event after buffering progress to update now playing")
	}
	if updated.bufferingPercent != nil {
		t.Fatal("expected buffering percentage to clear after track changed event")
	}
}

func TestLateBufferingProgressDoesNotOverridePlayingState(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.currentPlayID = 7
	model.app.IsPlaying = true
	model.app.NowPlaying = &app.NowPlaying{ID: "1", Title: "Song", Artist: "Artist"}

	nextModel, _ := model.Update(playbackProgressMsg{playID: 7, currentMS: 1000, totalMS: 180000})
	updated := nextModel.(Model)
	if got := updated.displayState(); got != "Playing" {
		t.Fatalf("expected playing state after playback progress, got %q", got)
	}

	nextModel, cmd := updated.Update(bufferingProgressMsg{playID: 7, percent: 100})
	updated = nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected late buffering event to keep listening")
	}
	if got := updated.displayState(); got != "Playing" {
		t.Fatalf("expected late buffering progress not to override playing state, got %q", got)
	}
	if updated.bufferingPercent != nil {
		t.Fatal("expected late buffering percentage to be ignored after playback starts")
	}
}

func TestTrackChangedStartsPlayingStateBeforeProgressEvent(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.currentPlayID = 7
	model.app.IsPlaying = true

	nextModel, cmd := model.Update(playbackTrackChangedMsg{
		playID:    7,
		meta:      deezer.TrackMetadata{ID: "1", Title: "Song", Artist: "Artist"},
		quality:   deezer.AudioQuality320,
		initialMS: 0,
	})
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected follow-up playback listener/tick command")
	}
	if got := updated.displayState(); got != "Playing" {
		t.Fatalf("expected track changed to switch state to playing, got %q", got)
	}
	if !updated.progressActive {
		t.Fatal("expected local progress ticking after track changed")
	}

	nextModel, _ = updated.Update(bufferingProgressMsg{playID: 7, percent: 100})
	updated = nextModel.(Model)
	if got := updated.displayState(); got != "Playing" {
		t.Fatalf("expected late buffering not to override track changed state, got %q", got)
	}
}

func TestStalePlaybackEventsKeepListenerAlive(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.currentPlayID = 2
	model.nextPlaybackID = 2
	model.playbackEvents <- playbackTrackChangedMsg{
		playID:  2,
		meta:    deezer.TrackMetadata{ID: "2", Title: "Current Song", Artist: "Artist"},
		quality: deezer.AudioQuality320,
	}

	nextModel, cmd := model.Update(bufferingProgressMsg{playID: 1, percent: 42})
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected stale buffering event to keep listening")
	}

	nextModel, _ = updated.Update(cmd())
	updated = nextModel.(Model)
	if updated.app.NowPlaying == nil || updated.app.NowPlaying.Title != "Current Song" {
		t.Fatal("expected listener to survive stale event and process current track")
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
	var bootstrapMsg tea.Msg
	switch typed := msg.(type) {
	case tea.BatchMsg:
		for _, cmd := range typed {
			if cmd == nil {
				continue
			}
			candidate := cmd()
			if _, ok := candidate.(bootstrapLoadedMsg); ok {
				bootstrapMsg = candidate
				break
			}
		}
	default:
		bootstrapMsg = msg
	}
	nextModel, cmd := model.Update(bootstrapMsg)
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
	if updated.app.ActivePanel != app.ActivePanelNavigation {
		t.Fatalf("expected focus to stay on navigation after initial home load, got %v", updated.app.ActivePanel)
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

func TestStartingNextTrackClearsStaleNowPlayingAndArtwork(t *testing.T) {
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, &fakePlaybackRuntime{})
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "Old Song", Artist: "A"},
		{ID: "2", Title: "New Song", Artist: "B"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(1)
	model.app.NowPlaying = &app.NowPlaying{ID: "1", Title: "Old Song", Artist: "A"}
	model.artworkANSI = "old-art"
	model.artworkURL = "https://example.invalid/old.jpg"

	cmd := model.startTrackPlayback("2")
	if cmd == nil {
		t.Fatal("expected playback command")
	}
	if model.app.NowPlaying != nil {
		t.Fatal("expected stale now-playing metadata to be cleared while buffering")
	}
	if model.artworkANSI != "" || model.artworkURL != "" {
		t.Fatal("expected stale artwork to be cleared while buffering")
	}

	model.width = 120
	view := model.renderStatusLine()
	if !strings.Contains(view, "New Song") {
		t.Fatal("expected queued next song title while buffering")
	}
	if strings.Contains(view, "Old Song") || strings.Contains(view, "old-art") {
		t.Fatal("did not expect stale song or artwork while buffering")
	}
}

func TestStatusLineRendersDefaultArtworkWhenArtworkMissing(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.width = 120
	model.app.NowPlaying = &app.NowPlaying{ID: "1", Title: "Song", Artist: "Artist"}
	model.artworkANSI = ""

	view := model.renderStatusLine()
	if !strings.Contains(view, "NO ART") || !strings.Contains(view, "+--------+") {
		t.Fatal("expected default artwork placeholder")
	}
}

func TestArtworkSlotDoesNotLeakCachedThemeBackground(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	applyTheme(config.ThemeAetheria)

	slot := model.renderArtworkSlot("X\x1b[39;48;2;40;40;40m", 16, 9)
	if strings.Contains(slot, "48;2;40;40;40") {
		t.Fatal("did not expect cached artwork reset background to leak into slot")
	}
	if !strings.Contains(slot, "48;2;21;17;31") {
		t.Fatal("expected artwork slot padding to use current theme background")
	}
}

func TestSelectingTrackInCurrentCollectionKeepsExistingQueue(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.app.ActivePanel = app.ActivePanelMain
	model.app.CurrentTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
		{ID: "3", Title: "Three", Artist: "C"},
	}
	model.app.QueueTracks = append([]app.Track(nil), model.app.CurrentTracks...)
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(0)
	model.app.MainState.Select(intPtr(2))

	nextModel, cmd := model.Update(tea.KeyPressMsg(tea.Key{Text: "enter"}))
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected playback command")
	}
	if updated.app.QueueIndex == nil || *updated.app.QueueIndex != 1 {
		t.Fatalf("expected queue index to switch to selected track, got %#v", updated.app.QueueIndex)
	}
	if len(updated.app.QueueTracks) != 3 {
		t.Fatalf("expected queue to stay intact, got %d tracks", len(updated.app.QueueTracks))
	}

	nextModel, _ = updated.Update(cmd())
	updated = nextModel.(Model)
	if len(runtime.started) != 1 || runtime.started[0] != "2" {
		t.Fatalf("expected selected queued track to start, got %#v", runtime.started)
	}
}

func TestPlaybackProgressPrebuffersNextQueuedTrack(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.currentPlayID = 3
	model.app.IsPlaying = true
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
		{ID: "3", Title: "Three", Artist: "C"},
	}
	model.app.QueueIndex = intPtr(0)
	model.app.NowPlaying = &app.NowPlaying{ID: "1", Title: "One", Artist: "A"}

	nextModel, _ := model.Update(playbackProgressMsg{playID: 3, currentMS: 0, totalMS: 180000})
	updated := nextModel.(Model)
	if len(runtime.prebuffered) != 1 || runtime.prebuffered[0] != "2" {
		t.Fatalf("expected next queued track to be prebuffered, got %#v", runtime.prebuffered)
	}
	if updated.app.StatusMessage != "Playing" {
		t.Fatalf("expected playing status, got %q", updated.app.StatusMessage)
	}
}

func TestLastFlowTrackStartsLoadingNextBatchForPrebuffer(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.app.IsFlowQueue = true
	model.app.FlowNextIndex = 2
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
	}
	model.app.QueueIndex = intPtr(1)

	cmd := model.maybePrebufferNextTrack()
	if cmd == nil {
		t.Fatal("expected last Flow track to start loading more Flow")
	}
	if !model.app.FlowLoadingMore {
		t.Fatal("expected FlowLoadingMore to be set")
	}
	if len(runtime.prebuffered) != 0 {
		t.Fatalf("did not expect prebuffer clear before next batch arrives, got %#v", runtime.prebuffered)
	}
}

func TestStartingLastFlowTrackLoadsNextBatchImmediately(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.app.IsFlowQueue = true
	model.app.FlowNextIndex = 2
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(1)

	cmd := model.startTrackPlayback("2")
	if cmd == nil {
		t.Fatal("expected playback command")
	}
	if !model.app.FlowLoadingMore {
		t.Fatal("expected starting last Flow track to request next batch immediately")
	}
}

func TestAppendedFlowBatchPrebuffersFirstNewTrack(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.app.IsPlaying = true
	model.app.IsFlowQueue = true
	model.app.FlowLoadingMore = true
	model.app.FlowNextIndex = 2
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(1)

	nextModel, _ := model.Update(collectionLoadedMsg{
		id:     "__flow__",
		title:  "Flow",
		tracks: []app.Track{{ID: "3", Title: "Three", Artist: "C"}, {ID: "4", Title: "Four", Artist: "D"}},
		isFlow: true,
		append: true,
	})
	updated := nextModel.(Model)

	if updated.app.FlowLoadingMore {
		t.Fatal("expected FlowLoadingMore to clear after append")
	}
	if !updated.app.IsPlaying {
		t.Fatal("expected background Flow append not to stop current playback")
	}
	if len(runtime.prebuffered) != 1 || runtime.prebuffered[0] != "3" {
		t.Fatalf("expected first appended Flow track to prebuffer, got %#v", runtime.prebuffered)
	}
}

func TestPlaybackProgressClearsPrebufferAtQueueEnd(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.currentPlayID = 4
	model.app.IsPlaying = true
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
	}
	model.app.QueueIndex = intPtr(0)
	model.app.NowPlaying = &app.NowPlaying{ID: "1", Title: "One", Artist: "A"}

	_, _ = model.Update(playbackProgressMsg{playID: 4, currentMS: 0, totalMS: 180000})
	if len(runtime.prebuffered) != 1 || runtime.prebuffered[0] != "" {
		t.Fatalf("expected prebuffer clear at queue end, got %#v", runtime.prebuffered)
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
