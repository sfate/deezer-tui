package tui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"deezer-tui/internal/app"
	"deezer-tui/internal/colorscheme"
	"deezer-tui/internal/config"
	"deezer-tui/internal/deezer"
	"deezer-tui/internal/player"
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

func firstNonTickMsg(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return msg
	}
	for _, batchCmd := range batch {
		msg := batchCmd()
		switch msg.(type) {
		case loadingTickMsg, prebufferTickMsg:
			continue
		default:
			return msg
		}
	}
	return nil
}

type fakePlaybackRuntime struct {
	started     []string
	qualities   []deezer.AudioQuality
	seeked      []uint64
	prebuffered [][]string
	session     *fakePlaybackSession
	startErr    error
	mediaEvents chan MediaControlCommand
	mediaStates []MediaControlState
}

func (f *fakePlaybackRuntime) Start(trackID string, quality deezer.AudioQuality, seekMS uint64, _ player.EventHandler) (PlaybackSession, error) {
	f.started = append(f.started, trackID)
	f.qualities = append(f.qualities, quality)
	f.seeked = append(f.seeked, seekMS)
	if f.startErr != nil {
		return nil, f.startErr
	}
	f.session = &fakePlaybackSession{}
	return f.session, nil
}

func (f *fakePlaybackRuntime) Prebuffer(trackIDs []string, _ deezer.AudioQuality, events chan<- tea.Msg) {
	f.prebuffered = append(f.prebuffered, append([]string(nil), trackIDs...))
	if events == nil {
		return
	}
	for _, trackID := range trackIDs {
		events <- prebufferStatusMsg{trackID: trackID, quality: deezer.AudioQuality320, status: PrebufferStatusLoading}
	}
}

func (f *fakePlaybackRuntime) MediaControlEvents() <-chan MediaControlCommand {
	return f.mediaEvents
}

func (f *fakePlaybackRuntime) UpdateMediaControl(state MediaControlState) {
	f.mediaStates = append(f.mediaStates, state)
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

func firstBatchMessage(cmd tea.Cmd) tea.Msg {
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			if child == nil {
				continue
			}
			return child()
		}
	}
	return msg
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
	if view.WindowTitle != "deezer-tui" {
		t.Fatalf("unexpected window title %q", view.WindowTitle)
	}
}

func TestViewShowsLoadingLogoBeforeInitialCollectionLoad(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.width = 120
	model.height = 40
	model.ready = false

	view := model.View().Content
	if !strings.Contains(view, "████") || !strings.Contains(view, "▄ ▄▖▄▖▄▖▄▖▄▖") {
		t.Fatal("expected filled wave-heart loading mark in startup view")
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

func TestFavoritesDefaultToNewestAddedFirstAndToggleSort(t *testing.T) {
	old := int64(1000)
	newer := int64(3000)
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.ready = true
	model.loadCollection("__favorites__", "Favorites", []app.Track{
		{ID: "old", Title: "Old", Artist: "A", AddedAtMS: &old},
		{ID: "new", Title: "New", Artist: "B", AddedAtMS: &newer},
	})

	if got := model.app.CurrentTracks[0].ID; got != "new" {
		t.Fatalf("expected newest favorite first by default, got %q", got)
	}

	nextModel, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "s"}))
	updated := nextModel.(Model)
	if got := updated.app.CurrentTracks[0].ID; got != "old" {
		t.Fatalf("expected oldest favorite first after sort toggle, got %q", got)
	}
	if !strings.Contains(updated.app.StatusMessage, "ascending") {
		t.Fatalf("expected ascending sort status, got %q", updated.app.StatusMessage)
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

func TestQueueRowsRenderPrebufferIndicators(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
		{ID: "3", Title: "Three", Artist: "C"},
		{ID: "4", Title: "Four", Artist: "D"},
		{ID: "5", Title: "Five", Artist: "E"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(0)
	model.app.IsPlaying = true
	model.setPrebufferStatus("2", deezer.AudioQuality320, PrebufferStatusScheduled)
	model.setPrebufferStatus("3", deezer.AudioQuality320, PrebufferStatusLoading)
	model.setPrebufferStatus("4", deezer.AudioQuality320, PrebufferStatusReady)

	view := model.renderQueue(80, 20)
	for _, want := range []string{"▶", "○", "◐", "✓", "-"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected queue indicator %q in view", want)
		}
	}
	if strings.Count(view, "▶") < 2 {
		t.Fatal("expected currently playing queue row to show both row marker and right-side indicator")
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

func TestLibraryPlaylistsScrollUnderFixedBrowseRows(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	for i := 1; i <= 8; i++ {
		model.app.Playlists = append(model.app.Playlists, app.Playlist{
			ID:    fmt.Sprintf("pl-%02d", i),
			Title: fmt.Sprintf("Playlist %02d", i),
		})
	}
	model.app.ActivePanel = app.ActivePanelPlaylists
	model.app.PlaylistState.Select(intPtr(5))

	view := model.renderSidebar(28, 12)

	if !strings.Contains(view, "Browse") || !strings.Contains(view, "Library") {
		t.Fatal("expected fixed browse and library headings")
	}
	if strings.Contains(view, "Playlist 01") {
		t.Fatal("did not expect first playlist to remain visible after scrolling")
	}
	if !strings.Contains(view, "Playlist 06") {
		t.Fatal("expected selected playlist to be visible after scrolling")
	}
}

func TestQueueScrollsTracksWithoutDroppingQueueInfo(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	for i := 1; i <= 12; i++ {
		model.app.QueueTracks = append(model.app.QueueTracks, app.Track{
			ID:     fmt.Sprintf("%02d", i),
			Title:  fmt.Sprintf("Track %02d", i),
			Artist: "Artist",
		})
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(8)
	model.app.QueueState.Select(intPtr(8))

	view := model.renderQueue(44, 18)

	if !strings.Contains(view, "State") || !strings.Contains(view, "Now Playing") || !strings.Contains(view, "Queue") {
		t.Fatal("expected queue metadata to remain visible")
	}
	if strings.Contains(view, "Track 01") {
		t.Fatal("did not expect first queue item to remain visible after current track scroll")
	}
	if !strings.Contains(view, "Track 09") || !strings.Contains(view, "Track 11") {
		t.Fatal("expected current queue item and following tracks to be visible")
	}
}

func TestCollectionTracksScrollUnderFixedHeader(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.app.ActivePanel = app.ActivePanelMain
	for i := 1; i <= 12; i++ {
		model.app.CurrentTracks = append(model.app.CurrentTracks, app.Track{
			ID:     fmt.Sprintf("%02d", i),
			Title:  fmt.Sprintf("Track %02d", i),
			Artist: "Artist",
		})
	}
	model.app.MainState.Select(intPtr(9))

	view := model.renderMain(58, 12)

	if !strings.Contains(view, "Play Collection") || !strings.Contains(view, "Title") {
		t.Fatal("expected collection controls and header to remain visible")
	}
	if strings.Contains(view, "Track 01") {
		t.Fatal("did not expect first track to remain visible after scrolling")
	}
	if !strings.Contains(view, "Track 09") {
		t.Fatal("expected selected track to be visible after scrolling")
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
	if updated.app.Config.Theme != colorscheme.Gruvbox {
		t.Fatalf("expected theme to cycle to gruvbox, got %s", updated.app.Config.Theme)
	}
	if len(saved) != 1 || saved[0].Theme != colorscheme.Gruvbox {
		t.Fatalf("expected gruvbox theme to be persisted, got %#v", saved)
	}
	if activePalette.Background != "#282828" {
		t.Fatalf("expected gruvbox palette to apply, got bg %s", activePalette.Background)
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

func TestBufferingProgressShowsPercentageAndStage(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.currentPlayID = 7

	nextModel, _ := model.Update(bufferingProgressMsg{playID: 7, percent: 42, stage: player.BufferingStageDownloading})
	updated := nextModel.(Model)
	if got := updated.displayState(); got != "Buffering 42% - Downloading..." {
		t.Fatalf("expected buffering percentage and stage, got %q", got)
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

func TestMediaControlToggleControlsSession(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	session := &fakePlaybackSession{}
	model.session = session
	model.app.IsPlaying = true

	nextModel, _ := model.Update(mediaControlCommandMsg{command: MediaControlCommand{Kind: MediaControlToggle}})
	updated := nextModel.(Model)
	if !session.paused || updated.app.IsPlaying {
		t.Fatal("expected media toggle to pause active playback")
	}
	if len(runtime.mediaStates) == 0 || runtime.mediaStates[len(runtime.mediaStates)-1].Playing {
		t.Fatalf("expected media state to sync paused playback, got %#v", runtime.mediaStates)
	}
}

func TestMediaControlSetPositionSeeksCurrentTrack(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.currentTrackID = "track-1"
	model.currentQuality = deezer.AudioQuality320
	model.app.NowPlaying = &app.NowPlaying{
		ID:        "track-1",
		Title:     "Song",
		Artist:    "Artist",
		Quality:   config.AudioQuality320,
		CurrentMS: 30_000,
		TotalMS:   120_000,
	}

	nextModel, cmd := model.Update(mediaControlCommandMsg{command: MediaControlCommand{Kind: MediaControlSetPosition, PositionMS: 55_000}})
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected media set-position command")
	}

	_, _ = updated.Update(firstBatchMessage(cmd))
	if len(runtime.seeked) != 1 || runtime.seeked[0] != 55_000 {
		t.Fatalf("expected media set-position to seek to 55s, got %#v", runtime.seeked)
	}
}

func TestPlaybackTrackChangedSyncsMediaControlMetadata(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.currentPlayID = 1
	model.app.IsPlaying = true
	durationSecs := uint64(180)
	artURL := "https://example.invalid/art.jpg"

	nextModel, _ := model.Update(playbackTrackChangedMsg{
		playID:    1,
		meta:      deezer.TrackMetadata{ID: "track-1", Title: "Song", Artist: "Artist", DurationSecs: &durationSecs, AlbumArtURL: &artURL},
		quality:   deezer.AudioQuality320,
		initialMS: 12_000,
	})
	_ = nextModel.(Model)

	if len(runtime.mediaStates) == 0 {
		t.Fatal("expected media state sync")
	}
	state := runtime.mediaStates[len(runtime.mediaStates)-1]
	if state.TrackID != "track-1" || state.Title != "Song" || state.Artist != "Artist" {
		t.Fatalf("unexpected metadata state: %#v", state)
	}
	if state.PositionMS != 12_000 || state.DurationMS != 180_000 || state.AlbumArtURL != artURL {
		t.Fatalf("unexpected timing/art state: %#v", state)
	}
}

func TestRepeatKeyCyclesRepeatMode(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})

	nextModel, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "r"}))
	updated := nextModel.(Model)
	if updated.app.RepeatMode != app.RepeatModeAll {
		t.Fatalf("expected repeat all, got %v", updated.app.RepeatMode)
	}

	nextModel, _ = updated.Update(tea.KeyPressMsg(tea.Key{Text: "r"}))
	updated = nextModel.(Model)
	if updated.app.RepeatMode != app.RepeatModeOne {
		t.Fatalf("expected repeat one, got %v", updated.app.RepeatMode)
	}

	nextModel, _ = updated.Update(tea.KeyPressMsg(tea.Key{Text: "r"}))
	updated = nextModel.(Model)
	if updated.app.RepeatMode != app.RepeatModeOff {
		t.Fatalf("expected repeat off, got %v", updated.app.RepeatMode)
	}
}

func TestSeekForwardRestartsCurrentTrackAtNewPosition(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.currentTrackID = "track-1"
	model.currentQuality = deezer.AudioQuality320
	model.app.NowPlaying = &app.NowPlaying{
		ID:        "track-1",
		Title:     "Song",
		Artist:    "Artist",
		Quality:   config.AudioQuality320,
		CurrentMS: 30_000,
		TotalMS:   120_000,
	}

	nextModel, cmd := model.Update(tea.KeyPressMsg(tea.Key{Text: "."}))
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected seek playback command")
	}

	_, _ = updated.Update(firstBatchMessage(cmd))
	if len(runtime.started) != 1 || runtime.started[0] != "track-1" {
		t.Fatalf("expected current track to restart, got %#v", runtime.started)
	}
	if len(runtime.seeked) != 1 || runtime.seeked[0] != 40_000 {
		t.Fatalf("expected seek to 40s, got %#v", runtime.seeked)
	}
}

func TestSeekBackwardClampsToStart(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.currentTrackID = "track-1"
	model.currentQuality = deezer.AudioQuality320
	model.app.NowPlaying = &app.NowPlaying{
		ID:        "track-1",
		Title:     "Song",
		Artist:    "Artist",
		Quality:   config.AudioQuality320,
		CurrentMS: 3_000,
		TotalMS:   120_000,
	}

	nextModel, cmd := model.Update(tea.KeyPressMsg(tea.Key{Text: ","}))
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected seek playback command")
	}

	_, _ = updated.Update(firstBatchMessage(cmd))
	if len(runtime.seeked) != 1 || runtime.seeked[0] != 0 {
		t.Fatalf("expected seek to clamp to start, got %#v", runtime.seeked)
	}
}

func TestSeekIsIgnoredForFlacPlayback(t *testing.T) {
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, &fakePlaybackRuntime{})
	model.currentTrackID = "track-1"
	model.currentQuality = deezer.AudioQualityFlac
	model.app.NowPlaying = &app.NowPlaying{
		ID:        "track-1",
		Title:     "Song",
		Artist:    "Artist",
		Quality:   config.AudioQualityFlac,
		CurrentMS: 30_000,
		TotalMS:   120_000,
	}

	nextModel, cmd := model.Update(tea.KeyPressMsg(tea.Key{Text: "."}))
	updated := nextModel.(Model)
	if cmd != nil {
		t.Fatal("did not expect seek command for FLAC")
	}
	if !strings.Contains(updated.app.StatusMessage, "FLAC") {
		t.Fatalf("expected FLAC seek status, got %q", updated.app.StatusMessage)
	}
}

func TestQualitySwitchDownPreservesPosition(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.currentTrackID = "track-1"
	model.currentQuality = deezer.AudioQuality320
	model.app.NowPlaying = &app.NowPlaying{
		ID:        "track-1",
		Title:     "Song",
		Artist:    "Artist",
		Quality:   config.AudioQuality320,
		CurrentMS: 45_000,
		TotalMS:   120_000,
	}

	nextModel, cmd := model.Update(tea.KeyPressMsg(tea.Key{Text: "u"}))
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected quality switch command")
	}

	_, _ = updated.Update(firstBatchMessage(cmd))
	if len(runtime.qualities) != 1 || runtime.qualities[0] != deezer.AudioQuality128 {
		t.Fatalf("expected switch to 128 kbps, got %#v", runtime.qualities)
	}
	if len(runtime.seeked) != 1 || runtime.seeked[0] != 45_000 {
		t.Fatalf("expected quality switch to preserve position, got %#v", runtime.seeked)
	}
}

func TestQualitySwitchToFlacRestartsAtBeginning(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.currentTrackID = "track-1"
	model.currentQuality = deezer.AudioQuality320
	model.app.NowPlaying = &app.NowPlaying{
		ID:        "track-1",
		Title:     "Song",
		Artist:    "Artist",
		Quality:   config.AudioQuality320,
		CurrentMS: 45_000,
		TotalMS:   120_000,
	}

	nextModel, cmd := model.Update(tea.KeyPressMsg(tea.Key{Text: "i"}))
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected quality switch command")
	}

	_, _ = updated.Update(firstBatchMessage(cmd))
	if len(runtime.qualities) != 1 || runtime.qualities[0] != deezer.AudioQualityFlac {
		t.Fatalf("expected switch to FLAC, got %#v", runtime.qualities)
	}
	if len(runtime.seeked) != 1 || runtime.seeked[0] != 0 {
		t.Fatalf("expected FLAC switch to restart from beginning, got %#v", runtime.seeked)
	}
}

func TestRepeatedNextStartsOnlyLatestRequestedTrack(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
		{ID: "3", Title: "Three", Artist: "C"},
		{ID: "4", Title: "Four", Artist: "D"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(0)

	nextModel, cmd := model.Update(tea.KeyPressMsg(tea.Key{Text: "n"}))
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected delayed next-track command")
	}
	firstRequest := updated.playbackRequest

	nextModel, _ = updated.Update(tea.KeyPressMsg(tea.Key{Text: "n"}))
	updated = nextModel.(Model)
	nextModel, _ = updated.Update(tea.KeyPressMsg(tea.Key{Text: "n"}))
	updated = nextModel.(Model)

	if updated.app.QueueIndex == nil || *updated.app.QueueIndex != 3 {
		t.Fatalf("expected queue index to advance to latest requested track, got %#v", updated.app.QueueIndex)
	}
	if len(runtime.started) != 0 {
		t.Fatalf("did not expect intermediate tracks to start, got %#v", runtime.started)
	}

	nextModel, staleCmd := updated.Update(scheduledPlaybackMsg{requestID: firstRequest})
	updated = nextModel.(Model)
	if staleCmd != nil {
		t.Fatal("did not expect stale scheduled playback to start")
	}

	nextModel, startCmd := updated.Update(scheduledPlaybackMsg{requestID: updated.playbackRequest})
	updated = nextModel.(Model)
	if startCmd == nil {
		t.Fatal("expected latest scheduled playback to start")
	}
	_, _ = updated.Update(firstBatchMessage(startCmd))
	if len(runtime.started) != 1 || runtime.started[0] != "4" {
		t.Fatalf("expected only latest requested track to start, got %#v", runtime.started)
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

func TestPlaybackFinishedRepeatsCurrentTrackInRepeatOneMode(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(1)
	model.app.RepeatMode = app.RepeatModeOne
	model.currentPlayID = 1

	nextModel, cmd := model.Update(playbackFinishedMsg{playID: 1, err: nil})
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected repeat-one playback command")
	}
	if updated.app.QueueIndex == nil || *updated.app.QueueIndex != 1 {
		t.Fatalf("expected queue index to stay on current track, got %#v", updated.app.QueueIndex)
	}

	nextModel, _ = updated.Update(firstBatchMessage(cmd))
	updated = nextModel.(Model)
	if len(runtime.started) != 1 || runtime.started[0] != "2" {
		t.Fatalf("expected current track to restart, got %#v", runtime.started)
	}
}

func TestPlaybackFinishedWrapsQueueInRepeatAllMode(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(1)
	model.app.RepeatMode = app.RepeatModeAll
	model.currentPlayID = 1

	nextModel, cmd := model.Update(playbackFinishedMsg{playID: 1, err: nil})
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected repeat-all playback command")
	}
	if updated.app.QueueIndex == nil || *updated.app.QueueIndex != 0 {
		t.Fatalf("expected queue index to wrap to first track, got %#v", updated.app.QueueIndex)
	}

	nextModel, _ = updated.Update(firstBatchMessage(cmd))
	updated = nextModel.(Model)
	if len(runtime.started) != 1 || runtime.started[0] != "1" {
		t.Fatalf("expected first track to start after wrap, got %#v", runtime.started)
	}
}

func TestPlaybackFinishedLoadsMoreFlowBeforeRepeatAllWrap(t *testing.T) {
	loader := &fakeLoader{
		flow: []app.Track{{ID: "3", Title: "Three", Artist: "C"}},
	}
	model := NewWithLoaderAndRuntime(config.Default(), loader, &fakePlaybackRuntime{})
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(1)
	model.app.RepeatMode = app.RepeatModeAll
	model.app.IsFlowQueue = true
	model.app.FlowNextIndex = 2
	model.currentPlayID = 1

	nextModel, cmd := model.Update(playbackFinishedMsg{playID: 1, err: nil})
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected Flow load command before repeat wrap")
	}
	if !updated.app.FlowLoadingMore {
		t.Fatal("expected Flow loading to be marked active")
	}
	if updated.app.QueueIndex == nil || *updated.app.QueueIndex != 1 {
		t.Fatalf("expected queue index to remain at Flow tail, got %#v", updated.app.QueueIndex)
	}
}

func TestCrossfadeProgressStartsNextTrackAndFadesCurrentSession(t *testing.T) {
	session := &fakePlaybackSession{faded: make(chan time.Duration, 1)}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, &fakePlaybackRuntime{})
	model.session = session
	model.currentPlayID = 1
	model.app.IsPlaying = true
	model.app.Config.CrossfadeEnabled = true
	model.app.Config.CrossfadeDurationMS = 5000
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(0)
	model.app.NowPlaying = &app.NowPlaying{ID: "1", Title: "One", Artist: "A", CurrentMS: 174_000, TotalMS: 180_000}

	nextModel, cmd := model.Update(playbackProgressMsg{playID: 1, currentMS: 176_000, totalMS: 180_000})
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected crossfade playback command")
	}
	if updated.app.QueueIndex == nil || *updated.app.QueueIndex != 1 {
		t.Fatalf("expected crossfade to advance queue index, got %#v", updated.app.QueueIndex)
	}
	if !updated.app.AutoTransitionArmed {
		t.Fatal("expected automatic transition to be armed")
	}
	select {
	case got := <-session.faded:
		if got != 5*time.Second {
			t.Fatalf("expected 5s fade, got %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("expected current session to fade out")
	}
}

func TestCrossfadeProgressRepeatsCurrentTrackInRepeatOneMode(t *testing.T) {
	session := &fakePlaybackSession{faded: make(chan time.Duration, 1)}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, &fakePlaybackRuntime{})
	model.session = session
	model.currentPlayID = 1
	model.app.IsPlaying = true
	model.app.Config.CrossfadeEnabled = true
	model.app.Config.CrossfadeDurationMS = 5000
	model.app.RepeatMode = app.RepeatModeOne
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(1)
	model.app.NowPlaying = &app.NowPlaying{ID: "2", Title: "Two", Artist: "B", CurrentMS: 176_000, TotalMS: 180_000}

	nextModel, cmd := model.Update(playbackProgressMsg{playID: 1, currentMS: 176_000, totalMS: 180_000})
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected repeat-one crossfade command")
	}
	if updated.app.QueueIndex == nil || *updated.app.QueueIndex != 1 {
		t.Fatalf("expected queue index to stay on repeated track, got %#v", updated.app.QueueIndex)
	}
	if updated.currentTrackID != "2" {
		t.Fatalf("expected current track to restart, got %q", updated.currentTrackID)
	}
}

func TestCrossfadeProgressWrapsQueueInRepeatAllMode(t *testing.T) {
	session := &fakePlaybackSession{faded: make(chan time.Duration, 1)}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, &fakePlaybackRuntime{})
	model.session = session
	model.currentPlayID = 1
	model.app.IsPlaying = true
	model.app.Config.CrossfadeEnabled = true
	model.app.Config.CrossfadeDurationMS = 5000
	model.app.RepeatMode = app.RepeatModeAll
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(1)
	model.app.NowPlaying = &app.NowPlaying{ID: "2", Title: "Two", Artist: "B", CurrentMS: 176_000, TotalMS: 180_000}

	nextModel, cmd := model.Update(playbackProgressMsg{playID: 1, currentMS: 176_000, totalMS: 180_000})
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected repeat-all crossfade command")
	}
	if updated.app.QueueIndex == nil || *updated.app.QueueIndex != 0 {
		t.Fatalf("expected queue index to wrap to first track, got %#v", updated.app.QueueIndex)
	}
	if updated.currentTrackID != "1" {
		t.Fatalf("expected first track to start after wrap, got %q", updated.currentTrackID)
	}
}

func TestCrossfadeProgressLoadsMoreFlowBeforeRepeatAllWrap(t *testing.T) {
	loader := &fakeLoader{
		flow: []app.Track{{ID: "3", Title: "Three", Artist: "C"}},
	}
	session := &fakePlaybackSession{faded: make(chan time.Duration, 1)}
	model := NewWithLoaderAndRuntime(config.Default(), loader, &fakePlaybackRuntime{})
	model.session = session
	model.currentPlayID = 1
	model.app.IsPlaying = true
	model.app.Config.CrossfadeEnabled = true
	model.app.Config.CrossfadeDurationMS = 5000
	model.app.RepeatMode = app.RepeatModeAll
	model.app.IsFlowQueue = true
	model.app.FlowNextIndex = 2
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(1)
	model.app.NowPlaying = &app.NowPlaying{ID: "2", Title: "Two", Artist: "B", CurrentMS: 176_000, TotalMS: 180_000}

	nextModel, cmd := model.Update(playbackProgressMsg{playID: 1, currentMS: 176_000, totalMS: 180_000})
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected Flow load command before crossfade repeat wrap")
	}
	if !updated.app.FlowLoadingMore {
		t.Fatal("expected Flow loading to be marked active")
	}
	if updated.app.QueueIndex == nil || *updated.app.QueueIndex != 1 {
		t.Fatalf("expected queue index to remain at Flow tail, got %#v", updated.app.QueueIndex)
	}
}

func TestCrossfadeProgressDoesNothingWhenDisabled(t *testing.T) {
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, &fakePlaybackRuntime{})
	model.session = &fakePlaybackSession{}
	model.currentPlayID = 1
	model.app.IsPlaying = true
	model.app.Config.CrossfadeEnabled = false
	model.app.Config.CrossfadeDurationMS = 5000
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(0)
	model.app.NowPlaying = &app.NowPlaying{ID: "1", Title: "One", Artist: "A", CurrentMS: 176_000, TotalMS: 180_000}

	nextModel, _ := model.Update(playbackProgressMsg{playID: 1, currentMS: 176_000, totalMS: 180_000})
	updated := nextModel.(Model)
	if updated.app.QueueIndex == nil || *updated.app.QueueIndex != 0 {
		t.Fatalf("did not expect queue index to advance, got %#v", updated.app.QueueIndex)
	}
	if updated.app.AutoTransitionArmed {
		t.Fatal("did not expect automatic transition to arm")
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
	applyTheme(colorscheme.Aetheria)

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
	if len(runtime.prebuffered) != 1 || !slices.Equal(runtime.prebuffered[0], []string{"2", "3"}) {
		t.Fatalf("expected next queued tracks to be prebuffered, got %#v", runtime.prebuffered)
	}
	if updated.app.StatusMessage != "Playing" {
		t.Fatalf("expected playing status, got %q", updated.app.StatusMessage)
	}
}

func TestHandleNextWrapsAtQueueEndWhenRepeatAllEnabled(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(1)
	model.app.RepeatMode = app.RepeatModeAll

	cmd := model.handleNext()
	if cmd == nil {
		t.Fatal("expected repeat-all next command")
	}
	if model.app.QueueIndex == nil || *model.app.QueueIndex != 0 {
		t.Fatalf("expected queue index to wrap to 0, got %#v", model.app.QueueIndex)
	}
	nextModel, playbackCmd := model.Update(cmd())
	updated := nextModel.(Model)
	if playbackCmd == nil {
		t.Fatal("expected playback command after scheduled repeat-all next")
	}
	_, _ = updated.Update(playbackCmd())
	if len(runtime.started) != 1 || runtime.started[0] != "1" {
		t.Fatalf("expected first track to start, got %#v", runtime.started)
	}
}

func TestLoadCollectionKeepsActiveFlowQueueState(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.ready = true
	model.app.IsFlowQueue = true
	model.app.FlowNextIndex = 12
	model.app.QueueTracks = []app.Track{{ID: "1", Title: "One", Artist: "A"}}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(0)

	model.loadCollection("__home__", "Home", []app.Track{{ID: "2", Title: "Two", Artist: "B"}})

	if !model.app.IsFlowQueue {
		t.Fatal("expected active Flow queue state to be preserved while browsing another collection")
	}
	if model.app.FlowNextIndex != 12 {
		t.Fatalf("expected FlowNextIndex to be preserved, got %d", model.app.FlowNextIndex)
	}
}

func TestSearchBackspaceRemovesWholeRune(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.app.IsSearching = true
	model.app.SearchQuery = "Beyoncé"

	nextModel, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	updated := nextModel.(Model)
	if updated.app.SearchQuery != "Beyonc" {
		t.Fatalf("expected whole rune removal, got %q", updated.app.SearchQuery)
	}
	if !utf8.ValidString(updated.app.SearchQuery) {
		t.Fatal("expected search query to remain valid UTF-8")
	}
}

func TestEnterSearchStartsLoadingAndLeavesInputMode(t *testing.T) {
	loader := &fakeLoader{
		search: SearchData{
			Tracks: []app.Track{{ID: "1", Title: "One", Artist: "A"}},
		},
	}
	model := NewWithLoader(config.Default(), loader)
	model.width = 100
	model.height = 36
	model.app.IsSearching = true
	model.app.ActivePanel = app.ActivePanelSearch
	model.app.SearchQuery = "artist"

	nextModel, cmd := model.Update(tea.KeyPressMsg(tea.Key{Text: "enter"}))
	updated := nextModel.(Model)
	if updated.app.IsSearching {
		t.Fatal("expected search input mode to close while request is loading")
	}
	if !updated.app.SearchLoading {
		t.Fatal("expected search loading state")
	}
	if updated.app.ActivePanel != app.ActivePanelMain {
		t.Fatalf("expected main panel to show search loading, got %v", updated.app.ActivePanel)
	}
	view := updated.renderMain(80, 14)
	if !strings.Contains(view, "Searching for") || !strings.Contains(view, "searching") || !strings.Contains(view, "⡿") {
		t.Fatal("expected search loading animation in main panel")
	}

	nextModel, _ = updated.Update(firstNonTickMsg(cmd))
	updated = nextModel.(Model)
	if updated.app.SearchLoading {
		t.Fatal("expected search loading to stop after results load")
	}
	if len(updated.app.CurrentTracks) != 1 {
		t.Fatal("expected fresh search results to load")
	}
}

func TestStartSearchWithoutLoaderDoesNotEnterLoadingState(t *testing.T) {
	model := NewWithLoader(config.Default(), nil)
	model.app.IsSearching = true
	model.app.SearchQuery = "artist"

	cmd := model.startSearch("artist")

	if cmd != nil {
		t.Fatal("did not expect a search command without a loader")
	}
	if model.app.IsSearching {
		t.Fatal("expected search input mode to close")
	}
	if model.app.SearchLoading {
		t.Fatal("did not expect search loading without a loader")
	}
	if !strings.Contains(model.app.StatusMessage, "Search unavailable") {
		t.Fatalf("expected unavailable status, got %q", model.app.StatusMessage)
	}
}

func TestStartSearchClearsStaleSearchResults(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.app.ShowingSearchResult = true
	model.app.SearchCategory = app.SearchCategoryPlaylists
	model.app.CurrentTracks = []app.Track{{ID: "old-track", Title: "Old", Artist: "A"}}
	model.app.SearchPlaylists = []app.Playlist{{ID: "old-playlist", Title: "Old Playlist"}}
	model.app.SearchArtists = []app.Artist{{ID: "old-artist", Name: "Old Artist"}}
	model.app.MainState.Select(intPtr(4))

	cmd := model.startSearch("new")

	if cmd == nil {
		t.Fatal("expected search command")
	}
	if len(model.app.CurrentTracks) != 0 || len(model.app.SearchPlaylists) != 0 || len(model.app.SearchArtists) != 0 {
		t.Fatalf("expected stale results to be cleared, got tracks=%d playlists=%d artists=%d", len(model.app.CurrentTracks), len(model.app.SearchPlaylists), len(model.app.SearchArtists))
	}
	if model.app.SearchCategory != app.SearchCategoryTracks {
		t.Fatalf("expected search category to reset to tracks, got %v", model.app.SearchCategory)
	}
	if selected := derefOrZero(model.app.MainState.Selected()); selected != 0 {
		t.Fatalf("expected selection reset to 0, got %d", selected)
	}
}

func TestSearchLoadingTickUsesSearchFrameCount(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.ready = true
	model.app.SearchLoading = true
	model.loadingFrame = len(loadingHeartFrames) - 1

	nextModel, cmd := model.Update(loadingTickMsg{})
	updated := nextModel.(Model)

	if cmd == nil {
		t.Fatal("expected loading tick to continue")
	}
	if updated.loadingFrame != len(loadingHeartFrames) {
		t.Fatalf("expected search frame counter to advance past heart frame count, got %d", updated.loadingFrame)
	}
}

func TestStaleSearchResultsAreIgnored(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})

	_ = model.startSearch("old")
	oldID := model.activeSearchID
	_ = model.startSearch("new")
	newID := model.activeSearchID
	if oldID == newID {
		t.Fatal("expected a new search request id")
	}

	nextModel, _ := model.Update(searchLoadedMsg{
		requestID: oldID,
		query:     "old",
		tracks:    []app.Track{{ID: "old", Title: "Old", Artist: "A"}},
	})
	updated := nextModel.(Model)
	if !updated.app.SearchLoading {
		t.Fatal("expected newer search to remain loading")
	}
	if updated.app.SearchQuery != "new" {
		t.Fatalf("expected stale result to be ignored, got query %q", updated.app.SearchQuery)
	}
}

func TestCollectionLoadInvalidatesPendingSearch(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	_ = model.startSearch("old")
	searchID := model.activeSearchID

	nextModel, _ := model.Update(collectionLoadedMsg{
		id:     "playlist",
		title:  "Playlist",
		tracks: []app.Track{{ID: "playlist-track", Title: "Playlist Track", Artist: "A"}},
	})
	updated := nextModel.(Model)
	if updated.activeSearchID != 0 {
		t.Fatalf("expected active search to be invalidated, got %d", updated.activeSearchID)
	}

	nextModel, _ = updated.Update(searchLoadedMsg{
		requestID: searchID,
		query:     "old",
		tracks:    []app.Track{{ID: "search-track", Title: "Search Track", Artist: "B"}},
	})
	updated = nextModel.(Model)

	if updated.app.ShowingSearchResult {
		t.Fatal("expected stale search result not to replace collection")
	}
	if len(updated.app.CurrentTracks) != 1 || updated.app.CurrentTracks[0].ID != "playlist-track" {
		t.Fatalf("expected playlist track to remain loaded, got %#v", updated.app.CurrentTracks)
	}
}

func TestFlowLoadInvalidatesPendingSearch(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	_ = model.startSearch("old")
	searchID := model.activeSearchID

	nextModel, _ := model.Update(collectionLoadedMsg{
		id:       "__flow__",
		title:    "Flow",
		tracks:   []app.Track{{ID: "flow-track", Title: "Flow Track", Artist: "A"}},
		isFlow:   true,
		autoplay: false,
	})
	updated := nextModel.(Model)
	if updated.activeSearchID != 0 {
		t.Fatalf("expected active search to be invalidated, got %d", updated.activeSearchID)
	}

	nextModel, _ = updated.Update(searchLoadedMsg{
		requestID: searchID,
		query:     "old",
		tracks:    []app.Track{{ID: "search-track", Title: "Search Track", Artist: "B"}},
	})
	updated = nextModel.(Model)

	if updated.app.ShowingSearchResult {
		t.Fatal("expected stale search result not to replace Flow")
	}
	if !updated.app.IsFlowQueue {
		t.Fatal("expected Flow queue state to remain active")
	}
	if len(updated.app.CurrentTracks) != 1 || updated.app.CurrentTracks[0].ID != "flow-track" {
		t.Fatalf("expected Flow track to remain loaded, got %#v", updated.app.CurrentTracks)
	}
}

func TestPrebufferReadyStatusSurvivesWindowShift(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.currentPlayID = 3
	model.app.IsPlaying = true
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
		{ID: "3", Title: "Three", Artist: "C"},
		{ID: "4", Title: "Four", Artist: "D"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(0)
	model.app.NowPlaying = &app.NowPlaying{ID: "1", Title: "One", Artist: "A"}
	model.setPrebufferStatus("2", deezer.AudioQuality320, PrebufferStatusReady)

	nextModel, _ := model.Update(playbackProgressMsg{playID: 3, currentMS: 0, totalMS: 180000})
	updated := nextModel.(Model)
	status, ok := updated.prebufferStatus("2")
	if !ok || status != PrebufferStatusReady {
		t.Fatalf("expected ready status to survive window refresh, got %v/%t", status, ok)
	}
}

func TestPlaybackTrackChangedMarksCurrentTrackReady(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.currentPlayID = 7
	model.app.IsPlaying = true
	model.app.QueueTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
	}
	model.app.Queue = formatQueue(model.app.QueueTracks)
	model.app.QueueIndex = intPtr(0)

	nextModel, _ := model.Update(playbackTrackChangedMsg{
		playID:  7,
		meta:    deezer.TrackMetadata{ID: "1", Title: "One", Artist: "A"},
		quality: deezer.AudioQuality320,
	})
	updated := nextModel.(Model)
	status, ok := updated.prebufferStatus("1")
	if !ok || status != PrebufferStatusReady {
		t.Fatalf("expected current track to be marked ready, got %v/%t", status, ok)
	}
	view := updated.renderQueue(80, 20)
	if !strings.Contains(view, "▶") {
		t.Fatal("expected current track indicator")
	}
}

func TestReadyPrebufferStatusesEvictOldestWhenCacheLimitIsReached(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	for i := 1; i <= prebufferCacheStatusLimit+1; i++ {
		model.setPrebufferStatus(fmt.Sprintf("%d", i), deezer.AudioQuality320, PrebufferStatusReady)
	}
	if _, ok := model.prebufferStatus("1"); ok {
		t.Fatal("expected oldest ready status to be evicted")
	}
	if status, ok := model.prebufferStatus(fmt.Sprintf("%d", prebufferCacheStatusLimit+1)); !ok || status != PrebufferStatusReady {
		t.Fatalf("expected newest ready status to remain, got %v/%t", status, ok)
	}
}

func TestCollectionLoadPrebuffersFirstTwentyQueuedTracks(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	tracks := make([]app.Track, 25)
	want := make([]string, 20)
	for i := range tracks {
		id := fmt.Sprintf("%d", i+1)
		tracks[i] = app.Track{ID: id, Title: "Song " + id, Artist: "Artist"}
		if i < len(want) {
			want[i] = id
		}
	}

	nextModel, _ := model.Update(collectionLoadedMsg{id: "playlist", title: "Playlist", tracks: tracks})
	updated := nextModel.(Model)
	if len(runtime.prebuffered) != 1 || !slices.Equal(runtime.prebuffered[0], want) {
		t.Fatalf("expected first twenty tracks to prebuffer, got %#v", runtime.prebuffered)
	}
	if len(updated.prebufferStatuses) != 20 {
		t.Fatalf("expected twenty prebuffer statuses, got %d", len(updated.prebufferStatuses))
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
	if len(runtime.prebuffered) != 1 || !slices.Equal(runtime.prebuffered[0], []string{"3", "4"}) {
		t.Fatalf("expected appended Flow tracks to prebuffer, got %#v", runtime.prebuffered)
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
	if len(runtime.prebuffered) != 1 || len(runtime.prebuffered[0]) != 0 {
		t.Fatalf("expected prebuffer clear at queue end, got %#v", runtime.prebuffered)
	}
}

func TestPlaybackErrorStopsPlayback(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.currentPlayID = 1
	model.currentTrackID = "track-1"
	model.currentQuality = deezer.AudioQuality320
	model.app.IsPlaying = true

	nextModel, cmd := model.Update(playbackFinishedMsg{playID: 1, err: errors.New("boom")})
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected retry command after playback error")
	}
	if !updated.app.IsPlaying {
		t.Fatal("expected playback to remain active while retrying")
	}
	if updated.playbackRetries != 1 {
		t.Fatalf("expected one retry attempt, got %d", updated.playbackRetries)
	}

	msg := firstBatchMessage(cmd)
	nextModel, _ = updated.Update(msg)
	updated = nextModel.(Model)
	if len(runtime.started) != 1 || runtime.started[0] != "track-1" {
		t.Fatalf("expected same track to be reloaded, got %#v", runtime.started)
	}
	if len(runtime.qualities) != 1 || runtime.qualities[0] != deezer.AudioQuality320 {
		t.Fatalf("expected first retry to keep quality, got %#v", runtime.qualities)
	}
}

func TestPlaybackRetryReducesQualityAfterReloadFails(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.currentPlayID = 2
	model.currentTrackID = "track-2"
	model.currentQuality = deezer.AudioQualityFlac
	model.playbackRetries = 1
	model.app.IsPlaying = true

	nextModel, cmd := model.Update(playbackFinishedMsg{playID: 2, err: errors.New("again")})
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected quality downgrade retry command")
	}

	msg := firstBatchMessage(cmd)
	nextModel, _ = updated.Update(msg)
	updated = nextModel.(Model)
	if len(runtime.qualities) != 1 || runtime.qualities[0] != deezer.AudioQuality320 {
		t.Fatalf("expected retry to downgrade to 320 kbps, got %#v", runtime.qualities)
	}
	if updated.currentQuality != deezer.AudioQuality320 {
		t.Fatalf("expected model quality to track downgrade, got %s", updated.currentQuality)
	}
}

func TestPlaybackRetryStopsAtLowestQuality(t *testing.T) {
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, &fakePlaybackRuntime{})
	model.currentPlayID = 3
	model.currentTrackID = "track-3"
	model.currentQuality = deezer.AudioQuality128
	model.playbackRetries = 1
	model.app.IsPlaying = true

	nextModel, cmd := model.Update(playbackFinishedMsg{playID: 3, err: errors.New("nope")})
	updated := nextModel.(Model)
	if cmd != nil {
		t.Fatal("did not expect retry below 128 kbps")
	}
	if updated.app.IsPlaying {
		t.Fatal("expected playback to stop at lowest quality")
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

func TestTabSwitchesSearchResultCategories(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.app.ShowingSearchResult = true
	model.app.SearchCategory = app.SearchCategoryTracks
	model.app.ActivePanel = app.ActivePanelMain
	model.app.MainState.Select(intPtr(3))

	nextModel, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	updated := nextModel.(Model)
	if updated.app.SearchCategory != app.SearchCategoryPlaylists {
		t.Fatalf("expected playlists category, got %v", updated.app.SearchCategory)
	}
	if updated.app.MainState.Selected() == nil || *updated.app.MainState.Selected() != 0 {
		t.Fatalf("expected selection reset to 0, got %v", updated.app.MainState.Selected())
	}
}

func TestSearchTabsHaveStableWidthAcrossCategories(t *testing.T) {
	tracks := renderSearchTabs(app.SearchCategoryTracks)
	playlists := renderSearchTabs(app.SearchCategoryPlaylists)
	artists := renderSearchTabs(app.SearchCategoryArtists)

	if textWidth(tracks) != textWidth(playlists) || textWidth(playlists) != textWidth(artists) {
		t.Fatalf("expected stable tab widths, got tracks=%d playlists=%d artists=%d", textWidth(tracks), textWidth(playlists), textWidth(artists))
	}
}

func TestSearchTabsRenderWithoutTrackResults(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.app.ShowingSearchResult = true
	model.app.SearchCategory = app.SearchCategoryPlaylists
	model.app.SearchPlaylists = []app.Playlist{{ID: "pl-1", Title: "Focus"}}
	model.app.ActivePanel = app.ActivePanelMain

	view := model.renderMain(80, 12)
	if !strings.Contains(view, "TRACKS") || !strings.Contains(view, "PLAYLISTS") || !strings.Contains(view, "Focus") {
		t.Fatal("expected search tabs and playlist results without track results")
	}
}

func TestSearchTrackResultsShowAlbumAndYearColumns(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.app.ShowingSearchResult = true
	model.app.SearchCategory = app.SearchCategoryTracks
	model.app.CurrentTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A", Album: "First Album", Year: "2024"},
	}
	model.app.ActivePanel = app.ActivePanelMain

	view := model.renderMain(90, 12)
	for _, want := range []string{"Album", "Year", "First Album", "2024"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected search track results to contain %q", want)
		}
	}
}

func TestSearchTrackResultsUseAvailablePanelWidth(t *testing.T) {
	model := NewWithLoader(config.Default(), &fakeLoader{})
	model.app.ShowingSearchResult = true
	model.app.SearchCategory = app.SearchCategoryTracks
	model.app.CurrentTracks = []app.Track{
		{
			ID:     "1",
			Title:  "A Long Search Result Title That Needs Extra Room",
			Artist: "A Long Artist",
			Album:  "A Long Album",
			Year:   "2024",
		},
	}
	model.app.ActivePanel = app.ActivePanelMain

	view := model.renderMain(110, 12)
	if !strings.Contains(view, "A Long Search Result Title That") {
		t.Fatal("expected search track table to use wider panel space")
	}
}

func TestEnterOnSearchTrackQueuesAllSearchTracksAtSelectedIndex(t *testing.T) {
	runtime := &fakePlaybackRuntime{}
	model := NewWithLoaderAndRuntime(config.Default(), &fakeLoader{}, runtime)
	model.app.ShowingSearchResult = true
	model.app.SearchCategory = app.SearchCategoryTracks
	model.app.CurrentTracks = []app.Track{
		{ID: "1", Title: "One", Artist: "A"},
		{ID: "2", Title: "Two", Artist: "B"},
		{ID: "3", Title: "Three", Artist: "C"},
	}
	model.app.ActivePanel = app.ActivePanelMain
	model.app.MainState.Select(intPtr(1))

	nextModel, cmd := model.Update(tea.KeyPressMsg(tea.Key{Text: "enter"}))
	updated := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected playback command")
	}
	if len(updated.app.QueueTracks) != 3 {
		t.Fatalf("expected full search result queue, got %d tracks", len(updated.app.QueueTracks))
	}
	if updated.app.QueueIndex == nil || *updated.app.QueueIndex != 1 {
		t.Fatalf("expected queue index 1, got %v", updated.app.QueueIndex)
	}

	nextModel, _ = updated.Update(cmd())
	_ = nextModel.(Model)
	if len(runtime.started) != 1 || runtime.started[0] != "2" {
		t.Fatalf("expected selected search track to start, got %#v", runtime.started)
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

	nextModel, _ = updated.Update(firstNonTickMsg(cmd))
	updated = nextModel.(Model)
	if len(loader.queries) == 0 || loader.queries[0] != "Aster Lane" {
		t.Fatal("expected loader search to receive the artist name")
	}
	if len(updated.app.CurrentTracks) != 1 {
		t.Fatal("expected artist follow-up search results to load")
	}
}
