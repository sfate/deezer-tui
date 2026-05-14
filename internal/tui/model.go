package tui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	cellansi "github.com/charmbracelet/x/ansi"

	"deezer-tui/internal/app"
	"deezer-tui/internal/colorscheme"
	"deezer-tui/internal/config"
	"deezer-tui/internal/deezer"
	"deezer-tui/internal/player"
)

var activePalette = colorscheme.Lookup(colorscheme.Aetheria).Palette

var prebufferSpinnerFrames = []string{"◐", "◓", "◑", "◒"}

var searchLoadingFrames = []string{"⡿", "⣟", "⣯", "⣷", "⣾", "⣽", "⣻", "⢿"}

const (
	seekStepMS                uint64 = 10_000
	manualPlaybackStartDelay         = 90 * time.Millisecond
	searchTimeout                    = 20 * time.Second
	prebufferWindowSize              = 20
	prebufferCacheStatusLimit        = 20
)

type bootstrapLoadedMsg struct {
	playlists []app.Playlist
}

type collectionLoadedMsg struct {
	id       string
	title    string
	tracks   []app.Track
	isFlow   bool
	append   bool
	autoplay bool
}

type loadFailedMsg struct {
	message string
}

type searchLoadedMsg struct {
	requestID int
	query     string
	tracks    []app.Track
	playlists []app.Playlist
	artists   []app.Artist
}

type searchFailedMsg struct {
	requestID int
	message   string
}

type playbackStartedMsg struct {
	playID  int
	session PlaybackSession
}

type playbackStartFailedMsg struct {
	playID  int
	trackID string
	quality deezer.AudioQuality
	err     error
}

type playbackTrackChangedMsg struct {
	playID    int
	meta      deezer.TrackMetadata
	quality   deezer.AudioQuality
	initialMS uint64
}

type bufferingProgressMsg struct {
	playID  int
	percent uint8
	stage   player.BufferingStage
}

type playbackProgressMsg struct {
	playID    int
	currentMS uint64
	totalMS   uint64
}

type playbackVisualizerMsg struct {
	playID int
	bands  []uint8
}

type playbackErrorMsg struct {
	playID int
	err    error
}

type playbackFinishedMsg struct {
	playID int
	err    error
}

type playbackTickMsg struct{}

type prebufferStatusMsg struct {
	trackID string
	quality deezer.AudioQuality
	status  PrebufferStatus
}

type prebufferTickMsg struct{}

type scheduledPlaybackMsg struct {
	requestID int
}

type artworkLoadedMsg struct {
	url string
	art string
}

type loadingTickMsg struct{}

type mediaControlCommandMsg struct {
	command MediaControlCommand
}

type Model struct {
	app               *app.App
	loader            Loader
	runtime           PlayerRuntime
	session           PlaybackSession
	playbackEvents    chan tea.Msg
	progressBaseMS    uint64
	progressSince     time.Time
	progressActive    bool
	bufferingPercent  *int
	pauseRequested    bool
	saveConfig        func(config.Config) error
	artworkURL        string
	artworkANSI       string
	artCache          map[string]string
	width             int
	height            int
	nextPlaybackID    int
	currentPlayID     int
	playbackRequest   int
	pendingTrackID    string
	pendingQuality    deezer.AudioQuality
	pendingSeekMS     uint64
	pendingReset      bool
	currentTrackID    string
	currentQuality    deezer.AudioQuality
	playbackRetries   int
	favoritesSortAsc  bool
	ready             bool
	loadingFrame      int
	nextSearchID      int
	activeSearchID    int
	prebufferStatuses map[string]PrebufferStatus
	prebufferReady    []string
	visualizerBands   []uint8
	visualizerPeaks   []float64
}

func New() Model {
	return NewWithConfig(config.Load())
}

func NewWithConfig(cfg config.Config) Model {
	cfg.Theme = colorscheme.Normalize(cfg.Theme)
	cfg.DisplayMode = config.NormalizeDisplayMode(cfg.DisplayMode, cfg.DisplayEnabled)
	cfg.DisplayEnabled = cfg.DisplayMode != config.DisplayModeOff
	applyTheme(cfg.Theme)
	state := app.New(cfg)

	var loader Loader
	status := "Set ARL in ~/.deezer-tui-config.json to load Deezer data"
	if strings.TrimSpace(cfg.ARL) != "" {
		deezerLoader, err := NewDeezerLoader(cfg)
		if err != nil {
			status = fmt.Sprintf("Deezer client error: %v", err)
		} else {
			loader = deezerLoader
			status = "Loading Deezer library..."
		}
	}

	state.StatusMessage = status
	return Model{
		app:               state,
		loader:            loader,
		runtime:           newPlayerRuntime(loader),
		playbackEvents:    make(chan tea.Msg, 32),
		saveConfig:        config.Save,
		artCache:          map[string]string{},
		prebufferStatuses: map[string]PrebufferStatus{},
		prebufferReady:    []string{},
		ready:             loader == nil,
	}
}

func NewWithLoader(cfg config.Config, loader Loader) Model {
	cfg.Theme = colorscheme.Normalize(cfg.Theme)
	cfg.DisplayMode = config.NormalizeDisplayMode(cfg.DisplayMode, cfg.DisplayEnabled)
	cfg.DisplayEnabled = cfg.DisplayMode != config.DisplayModeOff
	applyTheme(cfg.Theme)
	state := app.New(cfg)
	state.StatusMessage = "Loading Deezer library..."
	if loader == nil {
		state.StatusMessage = "No Deezer loader configured"
	}
	return Model{
		app:               state,
		loader:            loader,
		runtime:           newPlayerRuntime(loader),
		playbackEvents:    make(chan tea.Msg, 32),
		saveConfig:        config.Save,
		artCache:          map[string]string{},
		prebufferStatuses: map[string]PrebufferStatus{},
		prebufferReady:    []string{},
		ready:             loader == nil,
	}
}

func NewWithLoaderAndRuntime(cfg config.Config, loader Loader, runtime PlayerRuntime) Model {
	model := NewWithLoader(cfg, loader)
	model.runtime = runtime
	return model
}

func (m Model) Init() tea.Cmd {
	mediaCmd := m.listenMediaControlCmd()
	if m.loader == nil {
		return mediaCmd
	}
	return tea.Batch(bootstrapCmd(m.loader), loadingTickCmd(), mediaCmd)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case bootstrapLoadedMsg:
		m.app.Playlists = msg.playlists
		m.app.PlaylistState.Select(intPtr(0))
		m.app.StatusMessage = fmt.Sprintf("Loaded %d playlists", len(msg.playlists))
		return m, loadHomeCmd(m.loader)
	case collectionLoadedMsg:
		if msg.isFlow && msg.append {
			oldQueueLen := len(m.app.QueueTracks)
			result := m.app.AppendFlowTracks(msg.tracks, msg.autoplay)
			m.app.FlowLoadingMore = false
			if result.AppendedCount == 0 {
				m.app.StatusMessage = "Flow returned no new tracks"
				return m, nil
			}
			m.app.FlowNextIndex += len(msg.tracks)
			m.app.StatusMessage = fmt.Sprintf("Appended %d Flow tracks", result.AppendedCount)
			if result.AutoplayTrackID != nil {
				return m, m.startTrackPlayback(*result.AutoplayTrackID)
			}
			var prebufferCmd tea.Cmd
			if oldQueueLen < len(m.app.QueueTracks) {
				prebufferCmd = m.prebufferQueueWindowFrom(oldQueueLen)
			}
			return m, prebufferCmd
		}

		m.app.CurrentPlaylistID = stringPtr(msg.id)
		if msg.isFlow {
			m.cancelActiveSearch()
			m.app.FlowNextIndex = len(msg.tracks)
			m.app.IsFlowQueue = true
			trackID := m.app.LoadFlowTracks(msg.tracks, msg.autoplay)
			m.ready = true
			m.app.StatusMessage = fmt.Sprintf("Loaded %s (%d tracks)", msg.title, len(msg.tracks))
			if trackID != nil {
				_ = m.prebufferQueueWindowFrom(1)
				return m, m.startTrackPlayback(*trackID)
			}
			return m, m.prebufferQueueWindowFrom(0)
		}

		m.loadCollection(msg.id, msg.title, msg.tracks)
		m.ready = true
		if msg.id == "__home__" {
			m.app.ActivePanel = app.ActivePanelNavigation
		}
		return m, m.prebufferTrackWindowFrom(msg.tracks, 0)
	case loadFailedMsg:
		m.app.StatusMessage = msg.message
		m.app.FlowLoadingMore = false
		m.app.IsSearching = false
		m.app.SearchLoading = false
		m.activeSearchID = 0
		m.ready = true
		return m, nil
	case loadingTickMsg:
		if m.app.SearchLoading {
			m.loadingFrame = (m.loadingFrame + 1) % len(searchLoadingFrames)
			return m, loadingTickCmd()
		}
		if !m.ready {
			m.loadingFrame = (m.loadingFrame + 1) % len(loadingHeartFrames)
			return m, loadingTickCmd()
		}
		return m, nil
	case prebufferStatusMsg:
		if msg.quality != qualityFromConfig(m.app.Config.DefaultQuality) {
			return m, listenPlaybackEventCmd(m.playbackEvents)
		}
		m.setPrebufferStatus(msg.trackID, msg.quality, msg.status)
		if msg.status == PrebufferStatusLoading {
			return m, tea.Batch(listenPlaybackEventCmd(m.playbackEvents), prebufferTickCmd())
		}
		return m, listenPlaybackEventCmd(m.playbackEvents)
	case prebufferTickMsg:
		if !m.hasLoadingPrebuffer() {
			return m, nil
		}
		m.loadingFrame = (m.loadingFrame + 1) % len(prebufferSpinnerFrames)
		return m, prebufferTickCmd()
	case mediaControlCommandMsg:
		return m, tea.Batch(m.handleMediaControlCommand(msg.command), m.listenMediaControlCmd())
	case searchLoadedMsg:
		if msg.requestID != m.activeSearchID {
			return m, nil
		}
		m.app.IsSearching = false
		m.app.SearchLoading = false
		m.app.SearchQuery = msg.query
		m.app.CurrentTracks = msg.tracks
		m.app.SearchPlaylists = msg.playlists
		m.app.SearchArtists = msg.artists
		m.app.ShowingSearchResult = true
		m.app.SearchCategory = app.SearchCategoryTracks
		m.app.ActivePanel = app.ActivePanelMain
		m.app.MainState.Select(intPtr(0))
		m.app.CurrentPlaylistID = stringPtr("__search__")
		m.app.StatusMessage = fmt.Sprintf("Search: %q", msg.query)
		return m, nil
	case searchFailedMsg:
		if msg.requestID != m.activeSearchID {
			return m, nil
		}
		m.app.IsSearching = false
		m.app.SearchLoading = false
		m.app.StatusMessage = msg.message
		return m, nil
	case playbackStartedMsg:
		if msg.playID != m.nextPlaybackID {
			msg.session.Stop()
			return m, nil
		}
		m.session = msg.session
		m.currentPlayID = msg.playID
		m.session.SetVolume(float32(m.app.Volume) / 100)
		m.app.IsPlaying = true
		m.syncMediaControl()
		m.progressActive = false
		m.bufferingPercent = intPtr(0)
		m.progressBaseMS = 0
		m.progressSince = time.Time{}
		m.app.AutoTransitionArmed = false
		m.app.StatusMessage = "Buffering..."
		m.visualizerBands = nil
		m.visualizerPeaks = nil
		if m.pauseRequested {
			m.session.Pause()
			m.app.IsPlaying = false
			m.app.StatusMessage = "Paused"
		}
		return m, tea.Batch(
			waitPlaybackCmd(msg.playID, msg.session),
			listenPlaybackEventCmd(m.playbackEvents),
		)
	case playbackStartFailedMsg:
		if msg.playID != m.nextPlaybackID {
			return m, nil
		}
		return m, m.retryPlaybackAfterError(msg.err)
	case bufferingProgressMsg:
		if msg.playID != m.currentPlayID && msg.playID != m.nextPlaybackID {
			return m, listenPlaybackEventCmd(m.playbackEvents)
		}
		if m.progressActive || strings.EqualFold(strings.TrimSpace(m.app.StatusMessage), "Playing") {
			return m, listenPlaybackEventCmd(m.playbackEvents)
		}
		percent := int(msg.percent)
		m.bufferingPercent = &percent
		m.app.StatusMessage = bufferingStatusMessage(percent, msg.stage)
		return m, listenPlaybackEventCmd(m.playbackEvents)
	case playbackTrackChangedMsg:
		if msg.playID != m.currentPlayID && msg.playID != m.nextPlaybackID {
			return m, listenPlaybackEventCmd(m.playbackEvents)
		}
		totalMS := uint64(0)
		if msg.meta.DurationSecs != nil {
			totalMS = *msg.meta.DurationSecs * 1000
		}
		m.app.NowPlaying = &app.NowPlaying{
			ID:          msg.meta.ID,
			Title:       msg.meta.Title,
			Artist:      msg.meta.Artist,
			Quality:     configQualityFromDeezer(msg.quality),
			CurrentMS:   msg.initialMS,
			TotalMS:     totalMS,
			AlbumArtURL: msg.meta.AlbumArtURL,
		}
		if _, ok := m.runtime.(PrebufferingRuntime); ok {
			m.setPrebufferStatus(msg.meta.ID, msg.quality, PrebufferStatusReady)
		}
		m.bufferingPercent = nil
		m.progressBaseMS = msg.initialMS
		m.progressSince = time.Now()
		m.progressActive = m.app.IsPlaying
		m.visualizerBands = nil
		m.visualizerPeaks = nil
		if m.app.IsPlaying {
			m.app.StatusMessage = "Playing"
		}
		m.syncMediaControl()
		m.artworkANSI = ""
		m.artworkURL = ""
		nextCmd := listenPlaybackEventCmd(m.playbackEvents)
		if m.progressActive {
			nextCmd = tea.Batch(nextCmd, playbackTickCmd())
		}
		if msg.meta.AlbumArtURL != nil && strings.TrimSpace(*msg.meta.AlbumArtURL) != "" {
			m.artworkURL = *msg.meta.AlbumArtURL
			if cached, ok := m.artCache[m.artworkURL]; ok {
				m.artworkANSI = cached
				return m, nextCmd
			}
			return m, tea.Batch(
				nextCmd,
				fetchArtworkCmd(m.artworkURL, 14, 14),
			)
		}
		return m, nextCmd
	case playbackProgressMsg:
		if msg.playID != m.currentPlayID || m.app.NowPlaying == nil {
			return m, listenPlaybackEventCmd(m.playbackEvents)
		}
		m.app.NowPlaying.CurrentMS = msg.currentMS
		if msg.totalMS > 0 {
			m.app.NowPlaying.TotalMS = msg.totalMS
		}
		m.syncMediaControl()
		m.bufferingPercent = nil
		m.progressBaseMS = msg.currentMS
		m.progressSince = time.Now()
		m.progressActive = m.app.IsPlaying
		m.app.StatusMessage = "Playing"
		crossfadeCmd := m.maybeStartCrossfadeTransition(msg.currentMS, m.app.NowPlaying.TotalMS)
		if crossfadeCmd != nil {
			if m.progressActive {
				return m, tea.Batch(listenPlaybackEventCmd(m.playbackEvents), playbackTickCmd(), crossfadeCmd)
			}
			return m, tea.Batch(listenPlaybackEventCmd(m.playbackEvents), crossfadeCmd)
		}
		loadMoreCmd := m.maybePrebufferNextTrack()
		if m.progressActive {
			return m, tea.Batch(listenPlaybackEventCmd(m.playbackEvents), playbackTickCmd(), loadMoreCmd)
		}
		return m, tea.Batch(listenPlaybackEventCmd(m.playbackEvents), loadMoreCmd)
	case playbackErrorMsg:
		if msg.playID != m.currentPlayID && msg.playID != m.nextPlaybackID {
			return m, listenPlaybackEventCmd(m.playbackEvents)
		}
		return m, tea.Batch(m.retryPlaybackAfterError(msg.err), listenPlaybackEventCmd(m.playbackEvents))
	case playbackFinishedMsg:
		if msg.playID != m.currentPlayID {
			return m, nil
		}
		m.session = nil
		m.progressActive = false
		m.bufferingPercent = nil
		m.progressSince = time.Time{}
		m.visualizerBands = nil
		m.visualizerPeaks = nil
		if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
			return m, m.retryPlaybackAfterError(msg.err)
		}
		if errors.Is(msg.err, context.Canceled) {
			m.syncMediaControl()
			return m, nil
		}
		cmd := m.handlePlaybackFinished()
		m.syncMediaControl()
		return m, cmd
	case artworkLoadedMsg:
		if msg.url == "" || msg.url != m.artworkURL {
			return m, nil
		}
		m.artCache[msg.url] = msg.art
		m.artworkANSI = msg.art
		return m, nil
	case playbackTickMsg:
		if !m.progressActive || !m.app.IsPlaying || m.app.NowPlaying == nil {
			return m, nil
		}
		elapsed := uint64(time.Since(m.progressSince).Milliseconds())
		current := m.progressBaseMS + elapsed
		if m.app.NowPlaying.TotalMS > 0 && current > m.app.NowPlaying.TotalMS {
			current = m.app.NowPlaying.TotalMS
		}
		m.app.NowPlaying.CurrentMS = current
		m.syncMediaControl()
		crossfadeCmd := m.maybeStartCrossfadeTransition(current, m.app.NowPlaying.TotalMS)
		if crossfadeCmd != nil {
			return m, tea.Batch(playbackTickCmd(), crossfadeCmd)
		}
		return m, playbackTickCmd()
	case playbackVisualizerMsg:
		if msg.playID != m.currentPlayID || !m.app.IsPlaying || len(msg.bands) == 0 {
			return m, listenPlaybackEventCmd(m.playbackEvents)
		}
		m.visualizerBands = append(m.visualizerBands[:0], msg.bands...)
		m.updateVisualizerPeaks(msg.bands)
		return m, listenPlaybackEventCmd(m.playbackEvents)
	case scheduledPlaybackMsg:
		if msg.requestID != m.playbackRequest || strings.TrimSpace(m.pendingTrackID) == "" {
			return m, nil
		}
		trackID := m.pendingTrackID
		quality := m.pendingQuality
		seekMS := m.pendingSeekMS
		resetRetries := m.pendingReset
		m.pendingTrackID = ""
		return m, m.startTrackPlaybackWithQualityAndSeek(trackID, quality, seekMS, resetRetries)
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyPressMsg:
		if m.app.IsSearching {
			return m, m.handleSearchInput(msg)
		}
		switch msg.String() {
		case "ctrl+c", "q":
			if m.session != nil {
				m.session.Stop()
			}
			return m, tea.Quit
		case "esc":
			m.app.IsSearching = false
			m.app.SearchLoading = false
			m.activeSearchID = 0
			m.app.ViewingSettings = false
			m.app.ActivePanel = app.ActivePanelNavigation
			m.app.StatusMessage = "Library"
		case "tab":
			if m.app.ActivePanel == app.ActivePanelMain && m.app.ShowingSearchResult {
				m.app.SwitchSearchCategoryRight()
				return m, nil
			}
			m.cyclePanelForward()
		case "shift+tab":
			m.cyclePanelBackward()
		case "up", "k":
			m.app.HandleUp()
		case "down", "j":
			m.app.HandleDown()
		case "left", "h":
			if m.app.ActivePanel == app.ActivePanelMain && m.app.ViewingSettings {
				m.adjustSelectedSetting(-1)
				return m, nil
			}
			m.app.HandleLeft()
		case "right", "l":
			if m.app.ActivePanel == app.ActivePanelMain && m.app.ViewingSettings {
				m.adjustSelectedSetting(1)
				return m, nil
			}
			m.app.HandleRight()
		case "enter":
			return m, m.handleEnter()
		case " ", "space":
			return m, m.handleSpacebar()
		case "n":
			return m, m.handleNext()
		case "p":
			return m, m.handlePrevious()
		case "r":
			m.cycleRepeatMode()
		case ",":
			return m, m.seekCurrentTrack(-int64(seekStepMS))
		case ".":
			return m, m.seekCurrentTrack(int64(seekStepMS))
		case "u":
			return m, m.switchCurrentQuality(-1)
		case "i":
			return m, m.switchCurrentQuality(1)
		case "+":
			m.adjustVolume(5)
		case "-":
			m.adjustVolume(-5)
		case "s":
			if m.toggleFavoritesSort() {
				return m, nil
			}
		case "/":
			m.app.IsSearching = true
			m.app.SearchQuery = ""
			m.app.SearchLoading = false
			m.activeSearchID = 0
			m.app.ActivePanel = app.ActivePanelSearch
			m.app.StatusMessage = "Search: type query and press Enter"
		}
	}

	return m, nil
}

func (m Model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		view := tea.NewView("Loading deezer-tui...")
		view.AltScreen = true
		view.WindowTitle = "deezer-tui"
		return view
	}

	if !m.ready {
		view := tea.NewView(fillBackground(m.renderLoadingScreen(), m.width))
		view.AltScreen = true
		view.WindowTitle = "deezer-tui"
		return view
	}

	header := m.renderHeader()
	searchBar := m.renderSearchBar()
	contentHeight := max(10, m.height-20)
	sidebarWidth := min(28, max(22, m.width/5))
	middleAvailable := max(72, m.width-sidebarWidth-4)
	queueWidth := middleAvailable / 2
	mainWidth := middleAvailable - queueWidth
	body := joinColumns(
		m.renderSidebar(sidebarWidth, contentHeight),
		m.renderQueue(queueWidth, contentHeight),
		m.renderMain(mainWidth, contentHeight),
	)
	footer := m.renderPlaybar()
	status := m.renderStatusArea()

	content := strings.Join([]string{
		header,
		searchBar,
		body,
		status,
		footer,
	}, "\n")
	content = fillBackground(content, m.width)
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "deezer-tui"
	return view
}

func (m *Model) cyclePanelForward() {
	switch m.app.ActivePanel {
	case app.ActivePanelNavigation:
		m.app.ActivePanel = app.ActivePanelPlaylists
	case app.ActivePanelPlaylists:
		m.app.ActivePanel = app.ActivePanelQueue
	case app.ActivePanelQueue:
		m.app.ActivePanel = app.ActivePanelMain
	case app.ActivePanelMain:
		m.app.ActivePanel = app.ActivePanelPlayer
	case app.ActivePanelSearch:
		m.app.ActivePanel = app.ActivePanelMain
	default:
		m.app.ActivePanel = app.ActivePanelNavigation
	}
}

func (m *Model) cyclePanelBackward() {
	switch m.app.ActivePanel {
	case app.ActivePanelPlayer, app.ActivePanelPlayerInfo, app.ActivePanelPlayerProgress:
		m.app.ActivePanel = app.ActivePanelMain
	case app.ActivePanelMain:
		m.app.ActivePanel = app.ActivePanelQueue
	case app.ActivePanelQueue:
		m.app.ActivePanel = app.ActivePanelPlaylists
	case app.ActivePanelPlaylists:
		m.app.ActivePanel = app.ActivePanelNavigation
	case app.ActivePanelSearch:
		m.app.ActivePanel = app.ActivePanelQueue
	default:
		m.app.ActivePanel = app.ActivePanelPlayer
	}
}

func (m *Model) togglePlayPause() {
	if m.app.IsPlaying {
		if m.session != nil {
			m.session.Pause()
		} else {
			m.pauseRequested = true
		}
		if m.progressActive && m.app.NowPlaying != nil {
			elapsed := uint64(time.Since(m.progressSince).Milliseconds())
			m.progressBaseMS += elapsed
			m.app.NowPlaying.CurrentMS = m.progressBaseMS
		}
		m.progressActive = false
		m.progressSince = time.Time{}
		m.app.IsPlaying = false
		m.app.StatusMessage = "Paused"
		m.visualizerBands = nil
		m.visualizerPeaks = nil
		m.syncMediaControl()
		return
	}
	if m.session != nil {
		m.pauseRequested = false
		m.session.Resume()
		m.app.IsPlaying = true
		if m.app.NowPlaying != nil {
			m.progressSince = time.Now()
			m.progressActive = true
		}
		m.app.StatusMessage = "Playing"
		m.syncMediaControl()
		if m.progressActive {
			return
		}
		return
	}
}

func (m *Model) handleMediaControlCommand(command MediaControlCommand) tea.Cmd {
	switch command.Kind {
	case MediaControlPlay:
		if !m.app.IsPlaying {
			return m.playCurrentQueueTrackOrResume()
		}
	case MediaControlPause:
		if m.app.IsPlaying {
			m.togglePlayPause()
		}
	case MediaControlToggle:
		if m.session != nil || m.app.IsPlaying {
			m.togglePlayPause()
			return nil
		}
		return m.playCurrentQueueTrackOrResume()
	case MediaControlNext:
		return m.handleNext()
	case MediaControlPrevious:
		return m.handlePrevious()
	case MediaControlSetPosition:
		return m.seekCurrentTrackTo(command.PositionMS)
	}
	return nil
}

func (m *Model) playCurrentQueueTrackOrResume() tea.Cmd {
	if m.session != nil {
		m.togglePlayPause()
		return nil
	}
	if m.app.QueueIndex != nil && *m.app.QueueIndex >= 0 && *m.app.QueueIndex < len(m.app.QueueTracks) {
		m.app.QueueState.Select(intPtr(*m.app.QueueIndex))
		return m.startTrackPlayback(m.app.QueueTracks[*m.app.QueueIndex].ID)
	}
	if len(m.app.QueueTracks) > 0 {
		m.app.QueueIndex = intPtr(0)
		m.app.QueueState.Select(intPtr(0))
		return m.startTrackPlayback(m.app.QueueTracks[0].ID)
	}
	return nil
}

func (m *Model) handleSpacebar() tea.Cmd {
	if m.session != nil || m.app.IsPlaying {
		m.togglePlayPause()
		return nil
	}

	if track := m.selectedTrack(); track != nil {
		switch m.app.ActivePanel {
		case app.ActivePanelQueue:
			idx := derefOrZero(m.app.QueueState.Selected())
			if idx >= 0 && idx < len(m.app.QueueTracks) {
				m.app.QueueIndex = intPtr(idx)
				m.app.QueueState.Select(intPtr(idx))
				m.app.IsPlaying = true
				m.app.StatusMessage = fmt.Sprintf("Selected %s - %s", track.Title, track.Artist)
				return m.startTrackPlayback(track.ID)
			}
		case app.ActivePanelMain, app.ActivePanelSearch:
			if m.app.ShowingSearchResult && m.app.SearchCategory == app.SearchCategoryTracks {
				return m.playSearchTrack(derefOrZero(m.app.MainState.Selected()))
			}
			if m.app.ActivePanel == app.ActivePanelMain && !m.app.ShowingSearchResult {
				selected := derefOrZero(m.app.MainState.Selected())
				if selected > 0 {
					trackIndex := selected - 1
					if m.currentCollectionOwnsQueue() && trackIndex < len(m.app.QueueTracks) {
						m.app.QueueIndex = intPtr(trackIndex)
						m.app.QueueState.Select(intPtr(trackIndex))
						m.app.IsPlaying = true
						m.app.StatusMessage = fmt.Sprintf("Selected %s - %s", track.Title, track.Artist)
						return m.startTrackPlayback(track.ID)
					}
				}
			}
			m.app.QueueTracks = []app.Track{*track}
			m.app.Queue = formatQueue(m.app.QueueTracks)
			m.app.QueueIndex = intPtr(0)
			m.app.QueueState.Select(intPtr(0))
			m.app.IsPlaying = true
			m.app.IsFlowQueue = false
			m.app.StatusMessage = fmt.Sprintf("Selected %s - %s", track.Title, track.Artist)
			return m.startTrackPlayback(track.ID)
		}
	}

	switch m.app.ActivePanel {
	case app.ActivePanelMain, app.ActivePanelSearch, app.ActivePanelQueue:
		return m.handleEnter()
	default:
		if len(m.app.QueueTracks) > 0 && m.app.QueueIndex != nil && *m.app.QueueIndex < len(m.app.QueueTracks) {
			return m.startTrackPlayback(m.app.QueueTracks[*m.app.QueueIndex].ID)
		}
	}
	return nil
}

func (m *Model) handleEnter() tea.Cmd {
	switch m.app.ActivePanel {
	case app.ActivePanelNavigation:
		index := derefOrZero(m.app.NavState.Selected())
		switch index {
		case 0:
			return loadHomeCmd(m.loader)
		case 1:
			return loadFlowCmd(m.loader, 0, false, true)
		case 2:
			return loadExploreCmd(m.loader)
		case 3:
			return loadFavoritesCmd(m.loader)
		case 4:
			m.app.ViewingSettings = true
			m.app.ActivePanel = app.ActivePanelMain
			m.app.SettingsState.Select(intPtr(0))
			m.app.StatusMessage = "Settings"
		}
	case app.ActivePanelPlaylists:
		if len(m.app.Playlists) == 0 {
			return nil
		}
		idx := derefOrZero(m.app.PlaylistState.Selected())
		if idx >= len(m.app.Playlists) {
			return nil
		}
		pl := m.app.Playlists[idx]
		return loadPlaylistCmd(m.loader, pl.ID, pl.Title)
	case app.ActivePanelQueue:
		if len(m.app.QueueTracks) == 0 {
			return nil
		}
		idx := derefOrZero(m.app.QueueState.Selected())
		if idx < 0 || idx >= len(m.app.QueueTracks) {
			return nil
		}
		m.app.QueueIndex = intPtr(idx)
		m.app.QueueState.Select(intPtr(idx))
		m.app.IsPlaying = true
		m.app.StatusMessage = fmt.Sprintf("Selected %s - %s", m.app.QueueTracks[idx].Title, m.app.QueueTracks[idx].Artist)
		return m.startTrackPlayback(m.app.QueueTracks[idx].ID)
	case app.ActivePanelMain:
		if m.app.ViewingSettings {
			m.adjustSelectedSetting(1)
			return nil
		}
		if m.app.ShowingSearchResult {
			return m.handleSearchResultEnter()
		}
		if len(m.app.CurrentTracks) == 0 {
			return nil
		}
		selected := derefOrZero(m.app.MainState.Selected())
		if selected == 0 {
			m.app.QueueTracks = append([]app.Track(nil), m.app.CurrentTracks...)
			m.app.Queue = formatQueue(m.app.QueueTracks)
			m.app.QueueIndex = intPtr(0)
			m.app.QueueState.Select(intPtr(0))
			m.app.IsPlaying = true
			m.app.IsFlowQueue = m.app.CurrentPlaylistID != nil && *m.app.CurrentPlaylistID == "__flow__"
			m.app.StatusMessage = fmt.Sprintf("Queued %d tracks", len(m.app.QueueTracks))
			return m.startTrackPlayback(m.app.QueueTracks[0].ID)
		}
		trackIndex := selected - 1
		if trackIndex >= 0 && trackIndex < len(m.app.CurrentTracks) {
			track := m.app.CurrentTracks[trackIndex]
			if m.currentCollectionOwnsQueue() && trackIndex < len(m.app.QueueTracks) {
				m.app.QueueIndex = intPtr(trackIndex)
				m.app.QueueState.Select(intPtr(trackIndex))
				m.app.IsPlaying = true
				m.app.StatusMessage = fmt.Sprintf("Selected %s - %s", track.Title, track.Artist)
				return m.startTrackPlayback(track.ID)
			}
			m.app.QueueTracks = []app.Track{track}
			m.app.Queue = formatQueue(m.app.QueueTracks)
			m.app.QueueIndex = intPtr(0)
			m.app.QueueState.Select(intPtr(0))
			m.app.IsPlaying = true
			m.app.IsFlowQueue = false
			m.app.StatusMessage = fmt.Sprintf("Selected %s - %s", track.Title, track.Artist)
			return m.startTrackPlayback(track.ID)
		}
	}
	return nil
}

func (m *Model) handleSearchResultEnter() tea.Cmd {
	selected := derefOrZero(m.app.MainState.Selected())
	switch m.app.SearchCategory {
	case app.SearchCategoryTracks:
		if len(m.app.CurrentTracks) == 0 {
			return nil
		}
		return m.playSearchTrack(selected)
	case app.SearchCategoryPlaylists:
		if len(m.app.SearchPlaylists) == 0 {
			return nil
		}
		if selected >= len(m.app.SearchPlaylists) {
			selected = len(m.app.SearchPlaylists) - 1
		}
		pl := m.app.SearchPlaylists[selected]
		return loadPlaylistCmd(m.loader, pl.ID, pl.Title)
	case app.SearchCategoryArtists:
		if len(m.app.SearchArtists) == 0 {
			return nil
		}
		if selected >= len(m.app.SearchArtists) {
			selected = len(m.app.SearchArtists) - 1
		}
		artist := m.app.SearchArtists[selected]
		return m.startSearch(artist.Name)
	default:
		return nil
	}
}

func (m *Model) loadCollection(id, title string, tracks []app.Track) {
	wasFlowQueue := m.app.IsFlowQueue
	flowLoadingMore := m.app.FlowLoadingMore
	flowNextIndex := m.app.FlowNextIndex
	m.app.CurrentPlaylistID = stringPtr(id)
	m.app.CurrentTracks = append([]app.Track(nil), tracks...)
	if id == "__favorites__" {
		m.applyFavoritesSort()
	}
	m.app.MainState.Select(intPtr(0))
	m.app.SearchPlaylists = nil
	m.app.SearchArtists = nil
	m.app.ShowingSearchResult = false
	m.cancelActiveSearch()
	m.app.ViewingSettings = false
	if m.ready {
		m.app.ActivePanel = app.ActivePanelMain
	}
	if len(m.app.QueueTracks) == 0 || m.app.QueueIndex == nil {
		m.app.IsFlowQueue = false
		m.app.FlowLoadingMore = false
		m.app.FlowNextIndex = 0
	} else {
		m.app.IsFlowQueue = wasFlowQueue
		m.app.FlowLoadingMore = flowLoadingMore
		m.app.FlowNextIndex = flowNextIndex
	}
	m.app.StatusMessage = fmt.Sprintf("Loaded %s (%d tracks)", title, len(tracks))
}

func (m *Model) cancelActiveSearch() {
	m.activeSearchID = 0
	m.app.IsSearching = false
	m.app.SearchLoading = false
}

func (m *Model) playSearchTrack(selected int) tea.Cmd {
	if len(m.app.CurrentTracks) == 0 {
		return nil
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= len(m.app.CurrentTracks) {
		selected = len(m.app.CurrentTracks) - 1
	}
	track := m.app.CurrentTracks[selected]
	m.app.QueueTracks = append([]app.Track(nil), m.app.CurrentTracks...)
	m.app.Queue = formatQueue(m.app.QueueTracks)
	m.app.QueueIndex = intPtr(selected)
	m.app.QueueState.Select(intPtr(selected))
	m.app.IsPlaying = true
	m.app.IsFlowQueue = false
	m.app.StatusMessage = fmt.Sprintf("Queued %d search tracks, selected %s - %s", len(m.app.QueueTracks), track.Title, track.Artist)
	return m.startTrackPlayback(track.ID)
}

func (m *Model) toggleFavoritesSort() bool {
	if m.app.CurrentPlaylistID == nil || *m.app.CurrentPlaylistID != "__favorites__" || len(m.app.CurrentTracks) == 0 {
		return false
	}
	m.favoritesSortAsc = !m.favoritesSortAsc
	m.applyFavoritesSort()
	m.app.MainState.Select(intPtr(0))
	m.app.StatusMessage = fmt.Sprintf("Favorites sorted by added date %s", ternary(m.favoritesSortAsc, "ascending", "descending"))
	return true
}

func (m *Model) applyFavoritesSort() {
	queueOwned := m.currentCollectionOwnsQueue()
	sort.SliceStable(m.app.CurrentTracks, func(i, j int) bool {
		left := m.app.CurrentTracks[i].AddedAtMS
		right := m.app.CurrentTracks[j].AddedAtMS
		if left == nil && right == nil {
			return false
		}
		if left == nil {
			return false
		}
		if right == nil {
			return true
		}
		if m.favoritesSortAsc {
			return *left < *right
		}
		return *left > *right
	})
	if queueOwned {
		m.app.QueueTracks = append([]app.Track(nil), m.app.CurrentTracks...)
		m.app.Queue = formatQueue(m.app.QueueTracks)
	}
}

func (m Model) currentCollectionOwnsQueue() bool {
	if len(m.app.CurrentTracks) == 0 || len(m.app.QueueTracks) != len(m.app.CurrentTracks) {
		return false
	}
	for i := range m.app.CurrentTracks {
		if m.app.CurrentTracks[i].ID != m.app.QueueTracks[i].ID {
			return false
		}
	}
	return true
}

func (m *Model) handleSearchInput(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.app.IsSearching = false
		m.app.SearchLoading = false
		m.activeSearchID = 0
		m.app.ViewingSettings = false
		m.app.ActivePanel = app.ActivePanelNavigation
		m.app.StatusMessage = "Library"
		return nil
	case "enter":
		query := strings.TrimSpace(m.app.SearchQuery)
		if query == "" {
			m.app.IsSearching = false
			m.app.StatusMessage = "Search query is empty"
			return nil
		}
		return m.startSearch(query)
	case "backspace":
		m.app.SearchQuery = trimLastRune(m.app.SearchQuery)
		return nil
	}
	if len(msg.Text) > 0 {
		m.app.SearchQuery += msg.Text
	}
	return nil
}

func (m *Model) startSearch(query string) tea.Cmd {
	query = strings.TrimSpace(query)
	if query == "" {
		m.app.IsSearching = false
		m.app.SearchLoading = false
		m.app.StatusMessage = "Search query is empty"
		return nil
	}
	if m.loader == nil {
		m.app.IsSearching = false
		m.app.SearchLoading = false
		m.activeSearchID = 0
		m.app.StatusMessage = "Search unavailable: Deezer loader is not configured"
		return nil
	}
	m.nextSearchID++
	m.activeSearchID = m.nextSearchID
	m.app.IsSearching = false
	m.app.SearchLoading = true
	m.app.SearchQuery = query
	m.app.ViewingSettings = false
	m.app.ActivePanel = app.ActivePanelMain
	m.app.CurrentPlaylistID = stringPtr("__search__")
	m.app.CurrentTracks = nil
	m.app.SearchPlaylists = nil
	m.app.SearchArtists = nil
	m.app.SearchCategory = app.SearchCategoryTracks
	m.app.MainState.Select(intPtr(0))
	m.app.ShowingSearchResult = true
	m.app.StatusMessage = fmt.Sprintf("Searching for %q...", query)
	return tea.Batch(searchCmd(m.loader, m.activeSearchID, query), loadingTickCmd())
}

func (m Model) listenMediaControlCmd() tea.Cmd {
	runtime, ok := m.runtime.(MediaControlRuntime)
	if !ok {
		return nil
	}
	return listenMediaControlCmd(runtime.MediaControlEvents())
}

func (m Model) syncMediaControl() {
	runtime, ok := m.runtime.(MediaControlRuntime)
	if !ok {
		return
	}
	state := MediaControlState{
		Playing:     m.app.IsPlaying,
		Stopped:     !m.app.IsPlaying && m.session == nil,
		Volume:      m.app.Volume,
		CanNext:     m.canGoNext(),
		CanPrevious: m.canGoPrevious(),
		CanSeek:     m.app.NowPlaying != nil && m.currentQuality != deezer.AudioQualityFlac,
		RepeatMode:  appRepeatMode(m.app.RepeatMode),
	}
	if now := m.app.NowPlaying; now != nil {
		state.TrackID = now.ID
		state.Title = now.Title
		state.Artist = now.Artist
		state.PositionMS = now.CurrentMS
		state.DurationMS = now.TotalMS
		if now.AlbumArtURL != nil {
			state.AlbumArtURL = *now.AlbumArtURL
		}
	}
	runtime.UpdateMediaControl(state)
}

func (m Model) canGoNext() bool {
	if m.app.QueueIndex == nil || len(m.app.QueueTracks) == 0 {
		return false
	}
	current := *m.app.QueueIndex
	return current+1 < len(m.app.QueueTracks) || m.app.ShouldLoadMoreFlow() || m.app.RepeatMode == app.RepeatModeAll || m.app.RepeatMode == app.RepeatModeOne
}

func (m Model) canGoPrevious() bool {
	if m.app.QueueIndex == nil || len(m.app.QueueTracks) == 0 {
		return false
	}
	return *m.app.QueueIndex > 0 || m.app.RepeatMode == app.RepeatModeAll || m.app.RepeatMode == app.RepeatModeOne
}

func (m *Model) handleNext() tea.Cmd {
	if m.app.QueueIndex == nil || len(m.app.QueueTracks) == 0 {
		return nil
	}
	current := *m.app.QueueIndex
	if current+1 < len(m.app.QueueTracks) {
		nextIndex := current + 1
		m.app.QueueIndex = intPtr(nextIndex)
		m.app.QueueState.Select(intPtr(nextIndex))
		return m.scheduleTrackPlayback(m.app.QueueTracks[nextIndex].ID, qualityFromConfig(m.app.Config.DefaultQuality), 0, true)
	}
	if m.app.ShouldLoadMoreFlow() {
		m.app.FlowLoadingMore = true
		m.app.StatusMessage = "Loading more Flow..."
		return loadFlowCmd(m.loader, m.app.FlowNextIndex, true, true)
	}
	if m.app.RepeatMode == app.RepeatModeAll && len(m.app.QueueTracks) > 0 {
		m.app.QueueIndex = intPtr(0)
		m.app.QueueState.Select(intPtr(0))
		return m.scheduleTrackPlayback(m.app.QueueTracks[0].ID, qualityFromConfig(m.app.Config.DefaultQuality), 0, true)
	}
	if m.app.RepeatMode == app.RepeatModeOne && current >= 0 && current < len(m.app.QueueTracks) {
		return m.scheduleTrackPlayback(m.app.QueueTracks[current].ID, qualityFromConfig(m.app.Config.DefaultQuality), 0, true)
	}
	return nil
}

func (m *Model) handlePrevious() tea.Cmd {
	if m.app.QueueIndex == nil || len(m.app.QueueTracks) == 0 {
		return nil
	}
	current := *m.app.QueueIndex
	if current == 0 {
		return m.startTrackPlayback(m.app.QueueTracks[0].ID)
	}
	prevIndex := current - 1
	m.app.QueueIndex = intPtr(prevIndex)
	m.app.QueueState.Select(intPtr(prevIndex))
	return m.scheduleTrackPlayback(m.app.QueueTracks[prevIndex].ID, qualityFromConfig(m.app.Config.DefaultQuality), 0, true)
}

func (m *Model) adjustVolume(delta int) {
	next := int(m.app.Volume) + delta
	if next < 0 {
		next = 0
	}
	if next > 100 {
		next = 100
	}
	m.app.Volume = uint16(next)
	if m.session != nil {
		m.session.SetVolume(float32(m.app.Volume) / 100)
	}
	m.app.StatusMessage = fmt.Sprintf("Volume: %d%%", m.app.Volume)
	m.syncMediaControl()
}

func (m *Model) cycleRepeatMode() {
	m.app.RepeatMode = nextRepeatMode(m.app.RepeatMode)
	m.app.StatusMessage = fmt.Sprintf("Repeat: %s", repeatModeLabel(m.app.RepeatMode))
	m.syncMediaControl()
}

func (m *Model) adjustSelectedSetting(direction int) {
	switch derefOrZero(m.app.SettingsState.Selected()) {
	case 0:
		m.app.Config.Theme = colorscheme.Next(m.app.Config.Theme, direction)
		applyTheme(m.app.Config.Theme)
		m.app.StatusMessage = fmt.Sprintf("Theme: %s", colorscheme.Label(m.app.Config.Theme))
		m.persistConfig()
	case 1:
		m.adjustVolume(direction * 5)
	case 2:
		m.app.Config.DefaultQuality = nextQuality(m.app.Config.DefaultQuality, direction)
		m.app.StatusMessage = fmt.Sprintf("Quality: %s", qualityLabel(m.app.Config.DefaultQuality))
		m.persistConfig()
	case 3:
		m.app.Config.CrossfadeEnabled = !m.app.Config.CrossfadeEnabled
		m.app.StatusMessage = fmt.Sprintf("Crossfade: %s", onOff(m.app.Config.CrossfadeEnabled))
		m.persistConfig()
	case 4:
		m.app.Config.CrossfadeDurationMS = nextCrossfadeDuration(m.app.Config.CrossfadeDurationMS, direction)
		m.app.StatusMessage = fmt.Sprintf("Crossfade duration: %dms", m.app.Config.CrossfadeDurationMS)
		m.persistConfig()
	case 5:
		m.app.Config.DisplayMode = nextDisplayMode(m.app.Config.DisplayMode, direction)
		m.app.Config.DisplayEnabled = m.app.Config.DisplayMode != config.DisplayModeOff
		m.app.StatusMessage = fmt.Sprintf("Display: %s", displayModeLabel(m.app.Config.DisplayMode))
		m.persistConfig()
	}
}

func (m *Model) persistConfig() {
	if m.saveConfig == nil {
		return
	}
	if err := m.saveConfig(m.app.Config); err != nil {
		m.app.StatusMessage = fmt.Sprintf("Save config error: %v", err)
	}
}

func (m *Model) startTrackPlayback(trackID string) tea.Cmd {
	return m.startTrackPlaybackWithQualityAndSeek(trackID, qualityFromConfig(m.app.Config.DefaultQuality), 0, true)
}

func (m *Model) startTrackPlaybackWithQuality(trackID string, quality deezer.AudioQuality, resetRetries bool) tea.Cmd {
	return m.startTrackPlaybackWithQualityAndSeek(trackID, quality, 0, resetRetries)
}

func (m *Model) startTrackPlaybackAt(trackID string, quality deezer.AudioQuality, seekMS uint64, resetRetries bool) tea.Cmd {
	return m.startTrackPlaybackWithQualityAndSeek(trackID, quality, seekMS, resetRetries)
}

func (m *Model) scheduleTrackPlayback(trackID string, quality deezer.AudioQuality, seekMS uint64, resetRetries bool) tea.Cmd {
	if strings.TrimSpace(trackID) == "" {
		return nil
	}
	if m.runtime == nil {
		m.app.StatusMessage = "Playback runtime is not configured"
		return nil
	}
	if m.session != nil {
		m.stopCurrentSession()
		m.session = nil
	}
	m.currentPlayID = 0
	m.progressActive = false
	m.bufferingPercent = intPtr(0)
	m.progressBaseMS = 0
	m.progressSince = time.Time{}
	m.app.AutoTransitionArmed = false
	m.pauseRequested = false
	m.app.NowPlaying = nil
	m.artworkANSI = ""
	m.artworkURL = ""
	m.visualizerBands = nil
	m.visualizerPeaks = nil
	if resetRetries {
		m.playbackRetries = 0
	}
	m.playbackRequest++
	m.pendingTrackID = ""
	m.currentTrackID = trackID
	m.currentQuality = quality
	m.app.IsPlaying = true
	m.app.StatusMessage = "Starting playback..."
	m.playbackRequest++
	m.pendingTrackID = trackID
	m.pendingQuality = quality
	m.pendingSeekMS = seekMS
	m.pendingReset = resetRetries
	requestID := m.playbackRequest
	return tea.Tick(manualPlaybackStartDelay, func(time.Time) tea.Msg {
		return scheduledPlaybackMsg{requestID: requestID}
	})
}

func (m *Model) startTrackPlaybackWithQualityAndSeek(trackID string, quality deezer.AudioQuality, seekMS uint64, resetRetries bool) tea.Cmd {
	if strings.TrimSpace(trackID) == "" {
		return nil
	}
	if m.runtime == nil {
		m.app.StatusMessage = "Playback runtime is not configured"
		return nil
	}
	if m.session != nil {
		m.stopCurrentSession()
		m.session = nil
	}
	m.currentPlayID = 0
	m.progressActive = false
	m.bufferingPercent = intPtr(0)
	m.progressBaseMS = 0
	m.progressSince = time.Time{}
	m.app.AutoTransitionArmed = false
	m.pauseRequested = false
	m.app.NowPlaying = nil
	m.artworkANSI = ""
	m.artworkURL = ""
	m.visualizerBands = nil
	m.visualizerPeaks = nil
	if resetRetries {
		m.playbackRetries = 0
	}
	m.currentTrackID = trackID
	m.currentQuality = quality
	m.nextPlaybackID++
	playID := m.nextPlaybackID
	m.app.IsPlaying = true
	m.app.StatusMessage = "Starting playback..."
	return tea.Batch(
		startPlaybackCmdWithEvents(playID, trackID, m.runtime, quality, seekMS, m.playbackEvents),
		m.maybeLoadMoreFlowForTail(),
	)
}

func (m *Model) seekCurrentTrack(deltaMS int64) tea.Cmd {
	if m.app.NowPlaying == nil || strings.TrimSpace(m.currentTrackID) == "" {
		m.app.StatusMessage = "Nothing playing"
		return nil
	}
	if m.currentQuality == deezer.AudioQualityFlac {
		m.app.StatusMessage = "Seeking is not supported for FLAC playback"
		return nil
	}
	seekMS := m.currentPlaybackPositionMS()
	if deltaMS < 0 {
		step := uint64(-deltaMS)
		if step >= seekMS {
			seekMS = 0
		} else {
			seekMS -= step
		}
	} else {
		seekMS += uint64(deltaMS)
	}
	if total := m.app.NowPlaying.TotalMS; total > 0 && seekMS >= total {
		seekMS = total - 1
	}
	m.app.StatusMessage = fmt.Sprintf("Seeking to %s", formatClock(seekMS))
	return m.startTrackPlaybackAt(m.currentTrackID, m.currentQuality, seekMS, true)
}

func (m *Model) seekCurrentTrackTo(positionMS uint64) tea.Cmd {
	if m.app.NowPlaying == nil || strings.TrimSpace(m.currentTrackID) == "" {
		m.app.StatusMessage = "Nothing playing"
		return nil
	}
	if m.currentQuality == deezer.AudioQualityFlac {
		m.app.StatusMessage = "Seeking is not supported for FLAC playback"
		return nil
	}
	if total := m.app.NowPlaying.TotalMS; total > 0 && positionMS >= total {
		positionMS = total - 1
	}
	m.app.StatusMessage = fmt.Sprintf("Seeking to %s", formatClock(positionMS))
	return m.startTrackPlaybackAt(m.currentTrackID, m.currentQuality, positionMS, true)
}

func (m *Model) switchCurrentQuality(direction int) tea.Cmd {
	if m.app.NowPlaying == nil || strings.TrimSpace(m.currentTrackID) == "" {
		m.app.StatusMessage = "Nothing playing"
		return nil
	}
	next := nextDeezerQuality(m.currentQuality, direction)
	if next == m.currentQuality {
		m.app.StatusMessage = fmt.Sprintf("Quality: %s", qualityLabel(configQualityFromDeezer(m.currentQuality)))
		return nil
	}
	seekMS := m.currentPlaybackPositionMS()
	if next == deezer.AudioQualityFlac {
		seekMS = 0
	}
	m.app.StatusMessage = fmt.Sprintf("Quality: %s", qualityLabel(configQualityFromDeezer(next)))
	return m.startTrackPlaybackAt(m.currentTrackID, next, seekMS, true)
}

func (m Model) currentPlaybackPositionMS() uint64 {
	if m.app.NowPlaying == nil {
		return 0
	}
	current := m.app.NowPlaying.CurrentMS
	if m.progressActive && m.app.IsPlaying && !m.progressSince.IsZero() {
		current = m.progressBaseMS + uint64(time.Since(m.progressSince).Milliseconds())
	}
	if total := m.app.NowPlaying.TotalMS; total > 0 && current >= total {
		return total - 1
	}
	return current
}

func (m *Model) retryPlaybackAfterError(err error) tea.Cmd {
	trackID := strings.TrimSpace(m.currentTrackID)
	if trackID == "" {
		m.app.IsPlaying = false
		m.app.StatusMessage = fmt.Sprintf("Playback error: %v", err)
		return nil
	}

	if m.playbackRetries == 0 {
		m.playbackRetries++
		m.app.StatusMessage = fmt.Sprintf("Playback error, reloading track: %v", err)
		return m.startTrackPlaybackWithQuality(trackID, m.currentQuality, false)
	}

	if lower, ok := lowerPlaybackQuality(m.currentQuality); ok {
		m.playbackRetries++
		m.app.StatusMessage = fmt.Sprintf("Playback error, retrying at %s: %v", qualityLabel(configQualityFromDeezer(lower)), err)
		return m.startTrackPlaybackWithQuality(trackID, lower, false)
	}

	m.app.IsPlaying = false
	m.app.StatusMessage = fmt.Sprintf("Playback error: %v", err)
	return nil
}

func lowerPlaybackQuality(current deezer.AudioQuality) (deezer.AudioQuality, bool) {
	switch current {
	case deezer.AudioQualityFlac:
		return deezer.AudioQuality320, true
	case deezer.AudioQuality320:
		return deezer.AudioQuality128, true
	default:
		return "", false
	}
}

func (m *Model) stopCurrentSession() {
	if m.session == nil {
		return
	}
	if m.app.Config.CrossfadeEnabled && m.app.Config.CrossfadeDurationMS > 0 {
		if session, ok := m.session.(fadeStoppingSession); ok {
			go session.FadeOutStop(time.Duration(m.app.Config.CrossfadeDurationMS) * time.Millisecond)
			return
		}
	}
	m.session.Stop()
}

func (m *Model) maybeStartCrossfadeTransition(currentMS, totalMS uint64) tea.Cmd {
	if !m.app.Config.CrossfadeEnabled || m.app.Config.CrossfadeDurationMS == 0 {
		return nil
	}
	if m.app.AutoTransitionArmed || m.session == nil || m.app.QueueIndex == nil || len(m.app.QueueTracks) == 0 {
		return nil
	}
	if totalMS == 0 || currentMS >= totalMS {
		return nil
	}
	remainingMS := totalMS - currentMS
	if remainingMS > m.app.Config.CrossfadeDurationMS {
		return nil
	}

	cmd := m.nextPlaybackCommandForAutoTransition()
	if cmd == nil {
		return nil
	}
	m.app.AutoTransitionArmed = true
	return cmd
}

func (m *Model) nextPlaybackCommandForAutoTransition() tea.Cmd {
	current := *m.app.QueueIndex
	if current < 0 || current >= len(m.app.QueueTracks) {
		return nil
	}

	if m.app.RepeatMode == app.RepeatModeOne {
		m.app.QueueState.Select(intPtr(current))
		return m.startTrackPlayback(m.app.QueueTracks[current].ID)
	}

	if current+1 < len(m.app.QueueTracks) {
		nextIndex := current + 1
		m.app.QueueIndex = intPtr(nextIndex)
		m.app.QueueState.Select(intPtr(nextIndex))
		return m.startTrackPlayback(m.app.QueueTracks[nextIndex].ID)
	}

	if m.app.ShouldLoadMoreFlow() {
		m.app.FlowLoadingMore = true
		m.app.StatusMessage = "Loading more Flow..."
		return loadFlowCmd(m.loader, m.app.FlowNextIndex, true, true)
	}

	if m.app.RepeatMode == app.RepeatModeAll {
		m.app.QueueIndex = intPtr(0)
		m.app.QueueState.Select(intPtr(0))
		return m.startTrackPlayback(m.app.QueueTracks[0].ID)
	}

	return nil
}

func (m *Model) maybePrebufferNextTrack() tea.Cmd {
	if m.app.QueueIndex == nil || len(m.app.QueueTracks) == 0 {
		m.cancelPrebufferWindow()
		return nil
	}

	nextIndex := *m.app.QueueIndex + 1
	if nextIndex < 0 || nextIndex >= len(m.app.QueueTracks) {
		if cmd := m.maybeLoadMoreFlowForTail(); cmd != nil {
			return cmd
		}
		if m.app.RepeatMode == app.RepeatModeAll && len(m.app.QueueTracks) > 0 {
			return m.prebufferQueueWindowFrom(0)
		}
		if m.app.RepeatMode == app.RepeatModeOne && *m.app.QueueIndex >= 0 && *m.app.QueueIndex < len(m.app.QueueTracks) {
			return m.prebufferQueueWindowFrom(*m.app.QueueIndex)
		}
		m.cancelPrebufferWindow()
		return nil
	}

	return m.prebufferQueueWindowFrom(nextIndex)
}

func (m *Model) maybeLoadMoreFlowForTail() tea.Cmd {
	if !m.app.ShouldLoadMoreFlow() {
		return nil
	}
	m.app.FlowLoadingMore = true
	m.app.StatusMessage = "Loading more Flow..."
	return loadFlowCmd(m.loader, m.app.FlowNextIndex, true, false)
}

func (m *Model) prebufferQueueWindowFrom(start int) tea.Cmd {
	return m.prebufferTrackWindowFrom(m.app.QueueTracks, start)
}

func (m *Model) prebufferTrackWindowFrom(tracks []app.Track, start int) tea.Cmd {
	runtime, ok := m.runtime.(PrebufferingRuntime)
	if !ok {
		return nil
	}
	if start < 0 || start >= len(tracks) {
		runtime.Prebuffer(nil, qualityFromConfig(m.app.Config.DefaultQuality), m.playbackEvents)
		return nil
	}
	end := min(len(tracks), start+prebufferWindowSize)
	trackIDs := make([]string, 0, end-start)
	for _, track := range tracks[start:end] {
		if strings.TrimSpace(track.ID) == "" {
			continue
		}
		trackIDs = append(trackIDs, track.ID)
	}
	quality := qualityFromConfig(m.app.Config.DefaultQuality)
	for _, trackID := range trackIDs {
		m.setPrebufferStatus(trackID, quality, PrebufferStatusScheduled)
	}
	runtime.Prebuffer(trackIDs, quality, m.playbackEvents)
	if len(trackIDs) > 0 {
		return tea.Batch(prebufferTickCmd(), listenPlaybackEventCmd(m.playbackEvents))
	}
	return nil
}

func (m *Model) cancelPrebufferWindow() {
	if runtime, ok := m.runtime.(PrebufferingRuntime); ok {
		runtime.Prebuffer(nil, qualityFromConfig(m.app.Config.DefaultQuality), m.playbackEvents)
	}
}

func (m *Model) setPrebufferStatus(trackID string, quality deezer.AudioQuality, status PrebufferStatus) {
	if strings.TrimSpace(trackID) == "" {
		return
	}
	if m.prebufferStatuses == nil {
		m.prebufferStatuses = map[string]PrebufferStatus{}
	}
	key := prebufferKey(trackID, quality)
	if current, ok := m.prebufferStatuses[key]; ok && current == PrebufferStatusReady && status != PrebufferStatusFailed {
		return
	}
	m.prebufferStatuses[key] = status
	if status == PrebufferStatusReady {
		m.rememberReadyPrebuffer(key)
	}
}

func (m *Model) rememberReadyPrebuffer(key string) {
	for _, existing := range m.prebufferReady {
		if existing == key {
			return
		}
	}
	m.prebufferReady = append(m.prebufferReady, key)
	for len(m.prebufferReady) > prebufferCacheStatusLimit {
		evict := m.prebufferReady[0]
		m.prebufferReady = m.prebufferReady[1:]
		if status, ok := m.prebufferStatuses[evict]; ok && status == PrebufferStatusReady {
			delete(m.prebufferStatuses, evict)
		}
	}
}

func (m Model) prebufferStatus(trackID string) (PrebufferStatus, bool) {
	if strings.TrimSpace(trackID) == "" || m.prebufferStatuses == nil {
		return PrebufferStatusScheduled, false
	}
	status, ok := m.prebufferStatuses[prebufferKey(trackID, qualityFromConfig(m.app.Config.DefaultQuality))]
	return status, ok
}

func (m Model) hasLoadingPrebuffer() bool {
	for _, status := range m.prebufferStatuses {
		if status == PrebufferStatusLoading {
			return true
		}
	}
	return false
}

func (m *Model) updateVisualizerPeaks(bands []uint8) {
	if len(bands) == 0 {
		return
	}
	if len(m.visualizerPeaks) != len(bands) {
		m.visualizerPeaks = make([]float64, len(bands))
	}
	const peakFall = 0.035
	for i, band := range bands {
		level := float64(band) / 8
		if level >= m.visualizerPeaks[i] {
			m.visualizerPeaks[i] = level
			continue
		}
		m.visualizerPeaks[i] = math.Max(level, m.visualizerPeaks[i]-peakFall)
	}
}

func prebufferKey(trackID string, quality deezer.AudioQuality) string {
	return string(quality) + "\x00" + trackID
}

func trimLastRune(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	return string(runes[:len(runes)-1])
}

func (m *Model) handlePlaybackFinished() tea.Cmd {
	if m.app.QueueIndex == nil || len(m.app.QueueTracks) == 0 {
		m.app.IsPlaying = false
		m.app.StatusMessage = "Playback finished"
		return nil
	}

	current := *m.app.QueueIndex
	if current < 0 || current >= len(m.app.QueueTracks) {
		m.app.IsPlaying = false
		m.app.StatusMessage = "Playback finished"
		return nil
	}

	if m.app.RepeatMode == app.RepeatModeOne {
		m.app.QueueState.Select(intPtr(current))
		return m.startTrackPlayback(m.app.QueueTracks[current].ID)
	}

	if current+1 < len(m.app.QueueTracks) {
		nextIndex := current + 1
		m.app.QueueIndex = intPtr(nextIndex)
		m.app.QueueState.Select(intPtr(nextIndex))
		return m.startTrackPlayback(m.app.QueueTracks[nextIndex].ID)
	}

	if m.app.ShouldLoadMoreFlow() {
		m.app.FlowLoadingMore = true
		m.app.StatusMessage = "Loading more Flow..."
		return loadFlowCmd(m.loader, m.app.FlowNextIndex, true, true)
	}

	if m.app.RepeatMode == app.RepeatModeAll {
		m.app.QueueIndex = intPtr(0)
		m.app.QueueState.Select(intPtr(0))
		return m.startTrackPlayback(m.app.QueueTracks[0].ID)
	}

	m.app.IsPlaying = false
	m.app.StatusMessage = "Playback finished"
	return nil
}

func (m Model) renderSidebar(width, height int) string {
	nav := []string{sectionHeading("Browse", activePalette.Blue)}
	items := []string{"Home", "Flow", "Explore", "Favorites", "Settings"}
	selectedNav := derefOrZero(m.app.NavState.Selected())
	for i, item := range items {
		selected := i == selectedNav && m.app.ActivePanel == app.ActivePanelNavigation
		nav = append(nav, listRow(item, selected, activePalette.Blue))
	}
	nav = append(nav, "", sectionHeading("Library", activePalette.Purple))
	visiblePlaylists := max(0, height-2-len(nav))
	selectedPlaylist := derefOrZero(m.app.PlaylistState.Selected())
	start := scrollStart(selectedPlaylist, len(m.app.Playlists), visiblePlaylists, 0)
	end := min(len(m.app.Playlists), start+visiblePlaylists)
	for i := start; i < end; i++ {
		pl := m.app.Playlists[i]
		selected := i == derefOrZero(m.app.PlaylistState.Selected()) && m.app.ActivePanel == app.ActivePanelPlaylists
		nav = append(nav, listRow(truncate(pl.Title, 20), selected, activePalette.Purple))
	}
	return m.renderPanel("Library", strings.Join(nav, "\n"), m.app.ActivePanel == app.ActivePanelNavigation || m.app.ActivePanel == app.ActivePanelPlaylists, width, height)
}

func (m Model) renderMain(width, height int) string {
	var title string
	switch {
	case m.app.ViewingSettings:
		title = "Settings"
	case m.app.CurrentPlaylistID != nil:
		title = displayCollectionTitle(*m.app.CurrentPlaylistID)
	default:
		title = "Browse"
	}

	lines := []string{}
	if m.app.ViewingSettings {
		lines = append(lines, m.renderSettingsRows()...)
	} else if m.app.SearchLoading {
		lines = append(lines, m.renderSearchLoading(width, height)...)
	} else if m.app.ShowingSearchResult {
		lines = append(lines, renderSearchTabs(m.app.SearchCategory))
		lines = append(lines, "")
		selected := derefOrZero(m.app.MainState.Selected())
		switch m.app.SearchCategory {
		case app.SearchCategoryTracks:
			trackColumns := searchTrackColumns(max(12, width-2))
			lines = append(lines, tableHeader(formatSearchTrackHeader(trackColumns), activePalette.Orange))
			lines = append(lines, separatorLine(max(12, width-2), activePalette.Border))
			visibleRows := max(0, height-2-len(lines))
			start := scrollStart(selected, len(m.app.CurrentTracks), visibleRows, 0)
			end := min(len(m.app.CurrentTracks), start+visibleRows)
			for i := start; i < end; i++ {
				track := m.app.CurrentTracks[i]
				label := formatSearchTrackRow(i+1, track, trackColumns)
				lines = append(lines, trackRow(label, selected == i, activePalette.Aqua))
			}
			if len(m.app.CurrentTracks) == 0 {
				lines = append(lines, paint(" No tracks found", activePalette.TextMuted, ""))
			}
		case app.SearchCategoryPlaylists:
			lines = append(lines, tableHeader(" Playlist", activePalette.Orange))
			lines = append(lines, separatorLine(41, activePalette.Border))
			visibleRows := max(0, height-2-len(lines))
			start := scrollStart(selected, len(m.app.SearchPlaylists), visibleRows, 0)
			end := min(len(m.app.SearchPlaylists), start+visibleRows)
			for i := start; i < end; i++ {
				pl := m.app.SearchPlaylists[i]
				label := fmt.Sprintf(" %02d %s", i+1, truncate(pl.Title, 40))
				lines = append(lines, trackRow(label, selected == i, activePalette.Purple))
			}
			if len(m.app.SearchPlaylists) == 0 {
				lines = append(lines, paint(" No playlists found", activePalette.TextMuted, ""))
			}
		case app.SearchCategoryArtists:
			lines = append(lines, tableHeader(" Artist", activePalette.Orange))
			lines = append(lines, separatorLine(41, activePalette.Border))
			visibleRows := max(0, height-2-len(lines))
			start := scrollStart(selected, len(m.app.SearchArtists), visibleRows, 0)
			end := min(len(m.app.SearchArtists), start+visibleRows)
			for i := start; i < end; i++ {
				artist := m.app.SearchArtists[i]
				label := fmt.Sprintf(" %02d %s", i+1, truncate(artist.Name, 40))
				lines = append(lines, trackRow(label, selected == i, activePalette.Green))
			}
			if len(m.app.SearchArtists) == 0 {
				lines = append(lines, paint(" No artists found", activePalette.TextMuted, ""))
			}
		}
	} else if len(m.app.CurrentTracks) == 0 {
		lines = append(lines, "", paint(" No tracks loaded", activePalette.TextMuted, ""))
	} else {
		selected := derefOrZero(m.app.MainState.Selected())
		playAll := trackRow(" Play Collection", selected == 0, activePalette.Yellow)
		lines = append(lines, playAll)
		if m.app.CurrentPlaylistID != nil && *m.app.CurrentPlaylistID == "__favorites__" {
			lines = append(lines, paint(fmt.Sprintf(" Sort: added date %s", ternary(m.favoritesSortAsc, "asc", "desc")), activePalette.TextMuted, ""))
		}
		lines = append(lines, "")
		lines = append(lines, tableHeader(" #  Title                               Artist", activePalette.Orange))
		lines = append(lines, separatorLine(58, activePalette.Border))
		visibleRows := max(0, height-2-len(lines))
		selectedTrack := max0(selected - 1)
		start := 0
		if selected > 0 {
			start = scrollStart(selectedTrack, len(m.app.CurrentTracks), visibleRows, 0)
		}
		end := min(len(m.app.CurrentTracks), start+visibleRows)
		for i := start; i < end; i++ {
			track := m.app.CurrentTracks[i]
			label := fmt.Sprintf(" %02d %-35s %s", i+1, truncate(track.Title, 35), truncate(track.Artist, 18))
			lines = append(lines, trackRow(label, selected == i+1, activePalette.Aqua))
		}
	}

	return m.renderPanel(title, strings.Join(lines, "\n"), m.app.ActivePanel == app.ActivePanelMain || m.app.ActivePanel == app.ActivePanelSearch, width, height)
}

func (m Model) renderSearchLoading(width, height int) []string {
	frame := searchLoadingFrames[m.loadingFrame%len(searchLoadingFrames)]
	contentWidth := max(1, width-4)
	lines := []string{
		"",
		centerText(fmt.Sprintf("%s %s", paint(frame, activePalette.Aqua, ""), paint("searching", activePalette.TextMuted, "")), contentWidth),
		"",
	}
	status := fmt.Sprintf("Searching for %q", truncate(m.app.SearchQuery, max(8, contentWidth-18)))
	lines = append(lines, centerText(paint(status, activePalette.TextMuted, ""), contentWidth))
	if len(lines) >= height {
		return lines[:height]
	}
	topPad := max(0, (height-len(lines))/2)
	padded := make([]string, 0, min(height, len(lines)+topPad))
	for range topPad {
		padded = append(padded, "")
	}
	padded = append(padded, lines...)
	if len(padded) > height {
		return padded[:height]
	}
	return padded
}

func (m Model) renderQueue(width, height int) string {
	contentWidth := max(16, width-4)
	metaWidth := max(16, contentWidth-2)
	queueIndicatorWidth := 2
	queueRowTextWidth := max(8, contentWidth-2)
	lines := []string{
		kvLine("State", ternary(m.app.IsPlaying, "Playing", "Stopped"), activePalette.Green),
		kvLine("Volume", fmt.Sprintf("%d%%", m.app.Volume), activePalette.Aqua),
		kvLine("Repeat", repeatModeLabel(m.app.RepeatMode), activePalette.Orange),
		kvLine("Flow", fmt.Sprintf("%t", m.app.IsFlowQueue), activePalette.Purple),
	}

	if m.app.QueueIndex != nil && *m.app.QueueIndex < len(m.app.QueueTracks) {
		track := m.app.QueueTracks[*m.app.QueueIndex]
		lines = append(lines, "", sectionHeading("Now Playing", activePalette.Yellow), paint(" "+truncate(track.Title, metaWidth), activePalette.TextStrong, ""), paint(" "+truncate(track.Artist, metaWidth), activePalette.TextMuted, ""))
	} else {
		lines = append(lines, "", paint(" Nothing queued", activePalette.TextMuted, ""))
	}

	if len(m.app.Queue) > 0 {
		lines = append(lines, "", sectionHeading("Queue", activePalette.Orange))
		lines = append(lines, separatorLine(contentWidth, activePalette.Border))
		visibleRows := max(0, height-2-len(lines))
		target := -1
		following := 2
		if m.app.ActivePanel == app.ActivePanelQueue {
			target = derefOrZero(m.app.QueueState.Selected())
			following = 0
		} else if m.app.QueueIndex != nil {
			target = *m.app.QueueIndex
		}
		start := scrollStart(target, len(m.app.Queue), visibleRows, following)
		end := min(len(m.app.Queue), start+visibleRows)
		for i := start; i < end; i++ {
			item := m.app.Queue[i]
			indicator := m.queuePrebufferIndicator(i)
			prefix := fmt.Sprintf(" %02d ", i+1)
			itemWidth := max(1, queueRowTextWidth-textWidth(prefix)-queueIndicatorWidth)
			line := prefix + fitToWidth(item, itemWidth)
			if m.app.QueueIndex != nil && i == *m.app.QueueIndex && m.app.IsPlaying {
				line = playingQueueRow(line, indicator, queueIndicatorWidth)
			} else if m.app.ActivePanel == app.ActivePanelQueue && i == derefOrZero(m.app.QueueState.Selected()) {
				line = selectedQueueRow(line, indicator, queueIndicatorWidth)
			} else if m.app.QueueIndex != nil && i == *m.app.QueueIndex {
				line = queueRow(line, "▶ ", activePalette.Aqua, indicator, queueIndicatorWidth)
			} else {
				line = queueRow(line, "  ", activePalette.Text, indicator, queueIndicatorWidth)
			}
			lines = append(lines, line)
		}
	}

	return m.renderPanel("Queue", strings.Join(lines, "\n"), m.app.ActivePanel == app.ActivePanelQueue || m.app.ActivePanel == app.ActivePanelPlayer || m.app.ActivePanel == app.ActivePanelPlayerInfo || m.app.ActivePanel == app.ActivePanelPlayerProgress, width, height)
}

type queueIndicator struct {
	glyph string
	fg    string
}

func playingQueueRow(text string, indicator queueIndicator, indicatorWidth int) string {
	indicator.glyph = "▶"
	return paint("▶ ", activePalette.BackgroundHard, activePalette.Aqua) +
		paint(strings.TrimLeft(text, " "), activePalette.BackgroundHard, activePalette.Aqua) +
		paint(fitToWidth(indicator.glyph, indicatorWidth), activePalette.BackgroundHard, activePalette.Aqua)
}

func selectedQueueRow(text string, indicator queueIndicator, indicatorWidth int) string {
	return paint("> ", activePalette.Orange, "") +
		paint(strings.TrimLeft(text, " "), activePalette.TextStrong, "") +
		paint(fitToWidth(indicator.glyph, indicatorWidth), indicator.fg, "")
}

func queueRow(text, prefix, accent string, indicator queueIndicator, indicatorWidth int) string {
	return paint(prefix, accent, "") +
		paint(strings.TrimLeft(text, " "), activePalette.Text, "") +
		paint(fitToWidth(indicator.glyph, indicatorWidth), indicator.fg, "")
}

func (m Model) queuePrebufferIndicator(index int) queueIndicator {
	if m.app.QueueIndex != nil && index == *m.app.QueueIndex && m.app.IsPlaying {
		return queueIndicator{glyph: "▶", fg: activePalette.Aqua}
	}
	if index < 0 || index >= len(m.app.QueueTracks) {
		return queueIndicator{glyph: "-", fg: activePalette.TextMuted}
	}
	status, ok := m.prebufferStatus(m.app.QueueTracks[index].ID)
	if !ok {
		return queueIndicator{glyph: "-", fg: activePalette.TextMuted}
	}
	switch status {
	case PrebufferStatusScheduled:
		return queueIndicator{glyph: "○", fg: activePalette.TextMuted}
	case PrebufferStatusLoading:
		return queueIndicator{glyph: prebufferSpinnerFrames[m.loadingFrame%len(prebufferSpinnerFrames)], fg: activePalette.Yellow}
	case PrebufferStatusReady:
		return queueIndicator{glyph: "✓", fg: activePalette.Green}
	case PrebufferStatusFailed:
		return queueIndicator{glyph: "×", fg: activePalette.Orange}
	default:
		return queueIndicator{glyph: "-", fg: activePalette.TextMuted}
	}
}

func (m Model) renderSearchBar() string {
	query := m.app.SearchQuery
	if query == "" && m.app.IsSearching {
		query = paint("_", activePalette.Aqua, "")
	}
	label := paint(" Search ", activePalette.TextMuted, "")
	if m.app.IsSearching || m.app.SearchLoading {
		label = paint(" Search ", activePalette.Orange, "")
	}
	help := paint("tab switch | hjkl move | enter select | space play/pause | / search", activePalette.TextMuted, "")
	return renderLineBox(fmt.Sprintf("%s %s", label, query), help, m.width)
}

func (m Model) renderPlaybar() string {
	controls := " space play/pause | n/p next prev | ,/. seek | u/i quality | r repeat | +/- volume "
	if m.app.CurrentPlaylistID != nil && *m.app.CurrentPlaylistID == "__favorites__" {
		controls = " space play/pause | n/p next prev | ,/. seek | u/i quality | r repeat | s sort | +/- volume "
	}
	left := paint(controls, activePalette.Text, "")
	stateColor := activePalette.TextMuted
	if m.app.IsPlaying {
		stateColor = activePalette.Orange
	}
	right := fmt.Sprintf("%s | %s",
		paint(fmt.Sprintf("vol %d%%", m.app.Volume), activePalette.Aqua, ""),
		paint(ternary(m.app.IsPlaying, "playing", "paused"), stateColor, ""),
	)
	return renderLineBox(left, right, m.width)
}

func (m Model) renderHeader() string {
	left := " deezer-tui "
	center := fmt.Sprintf(" %s ", displayCollectionTitle(derefString(m.app.CurrentPlaylistID, "Browse")))
	right := fmt.Sprintf(" %s | q quit ", activePanelLabel(m.app.ActivePanel))
	return renderTripleLine(left, center, right, m.width)
}

func (m Model) renderStatusArea() string {
	if displayMode(m.app.Config) == config.DisplayModeOff || m.width < 64 {
		return m.renderStatusLine()
	}
	gap := 2
	statusWidth := max(30, (m.width-gap)/2)
	displayWidth := max(30, m.width-gap-statusWidth)
	return joinColumns(
		m.renderStatusPanel(statusWidth),
		m.renderDisplayPanel(displayWidth),
	)
}

func (m Model) renderStatusLine() string {
	return m.renderStatusPanel(m.width)
}

func (m Model) renderStatusPanel(width int) string {
	title := "Nothing playing"
	artist := "-"
	progress := renderProgress(0, 0, max(20, min(48, width-26)))
	source := displayCollectionTitle(derefString(m.app.CurrentPlaylistID, "Browse"))
	quality := "-"
	elapsed := "00:00"
	total := "00:00"
	queueInfo := "-"
	if m.app.NowPlaying != nil {
		title = truncate(m.app.NowPlaying.Title, max(20, width-22))
		artist = truncate(m.app.NowPlaying.Artist, max(20, width-22))
		progress = renderProgress(m.app.NowPlaying.CurrentMS, m.app.NowPlaying.TotalMS, max(20, min(48, width-26)))
		quality = qualityLabel(m.app.NowPlaying.Quality)
		elapsed = formatClock(m.app.NowPlaying.CurrentMS)
		total = formatClock(m.app.NowPlaying.TotalMS)
	} else if m.app.QueueIndex != nil && *m.app.QueueIndex < len(m.app.QueueTracks) {
		track := m.app.QueueTracks[*m.app.QueueIndex]
		title = truncate(track.Title, max(20, width-22))
		artist = truncate(track.Artist, max(20, width-22))
	}
	if m.app.QueueIndex != nil && len(m.app.QueueTracks) > 0 {
		queueInfo = fmt.Sprintf("%d/%d", *m.app.QueueIndex+1, len(m.app.QueueTracks))
	}
	lines := []string{
		kvLine("State", m.displayState(), activePalette.Green),
		kvLine("Track", title, activePalette.Yellow),
		kvLine("Artist", artist, activePalette.Aqua),
		kvLine("Progress", progress, activePalette.Text),
		kvLine("Elapsed", elapsed+" / "+total, activePalette.Orange),
		kvLine("Quality", quality, activePalette.Purple),
		kvLine("Source", source, activePalette.Blue),
		kvLine("Queue", queueInfo, activePalette.TextMuted),
	}
	art := m.artworkANSI
	if art == "" {
		art = defaultArtworkANSI()
	}
	body := joinColumns(
		" ",
		m.renderArtworkSlot(art, 16, 9),
		m.renderTextSlot(strings.Join(lines, "\n"), max(24, width-24), 9, 1, 1),
	)
	return m.renderPanel("Status", body, m.app.IsPlaying || m.app.NowPlaying != nil, width, 11)
}

func (m Model) renderDisplayPanel(width int) string {
	body := m.renderDisplayBody(max(1, width-2), 9)
	return m.renderPanel("Display", body, displayMode(m.app.Config) != config.DisplayModeOff && len(m.visualizerBands) > 0, width, 11)
}

func (m Model) renderPanel(title, body string, active bool, width, height int) string {
	h := "─"
	v := "│"
	tl := "┌"
	tr := "┐"
	bl := "└"
	br := "┘"
	if active {
		h = "═"
		v = "║"
		tl = "╔"
		tr = "╗"
		bl = "╚"
		br = "╝"
	}
	innerWidth := max(12, width-2)
	bodyHeight := max(1, height-2)
	titleText := " " + truncate(title, max(1, innerWidth-2)) + " "
	borderFG := activePalette.Border
	titleFG := activePalette.TextMuted
	titleBG := activePalette.Background
	panelBG := activePalette.Background
	contentFG := activePalette.Text
	if active {
		borderFG = activePalette.Border
		titleFG = activePalette.Orange
		titleBG = activePalette.Background
		panelBG = activePalette.Background
		contentFG = activePalette.TextStrong
	}
	top := paint(tl, borderFG, panelBG) +
		paint(titleText, titleFG, titleBG) +
		paint(strings.Repeat(h, max(0, innerWidth-textWidth(titleText))), borderFG, panelBG) +
		paint(tr, borderFG, panelBG)
	lines := strings.Split(body, "\n")
	for len(lines) < bodyHeight {
		lines = append(lines, "")
	}
	framed := []string{top}
	for i := 0; i < bodyHeight; i++ {
		framed = append(framed,
			paint(v, borderFG, panelBG)+
				paint(fitToWidth(lines[i], innerWidth), contentFG, panelBG)+
				paint(v, borderFG, panelBG),
		)
	}
	framed = append(framed, paint(bl+strings.Repeat(h, innerWidth)+br, borderFG, panelBG))
	return strings.Join(framed, "\n")
}

func (m Model) renderFixedBlock(content string, width, height int) string {
	lines := strings.Split(content, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	out := make([]string, 0, height)
	for i := 0; i < height; i++ {
		out = append(out, fitToWidth(lines[i], width))
	}
	return strings.Join(out, "\n")
}

func (m Model) renderArtworkSlot(content string, width, height int) string {
	lines := strings.Split(content, "\n")
	for len(lines) < 7 {
		lines = append(lines, "")
	}
	out := make([]string, 0, height)
	topPad := 1
	bottomPad := 1
	sidePad := 1
	for i := 0; i < topPad; i++ {
		out = append(out, paint(strings.Repeat(" ", width), "", activePalette.Background))
	}
	for i := 0; i < 7; i++ {
		line := fitArtworkLine(lines[i], 14)
		out = append(out, paint(strings.Repeat(" ", sidePad), "", activePalette.Background)+line+paint(strings.Repeat(" ", sidePad), "", activePalette.Background))
	}
	for i := 0; i < bottomPad; i++ {
		out = append(out, paint(strings.Repeat(" ", width), "", activePalette.Background))
	}
	return strings.Join(out, "\n")
}

func fitArtworkLine(line string, width int) string {
	line = stripTrailingSGR(truncate(line, width))
	pad := max(0, width-textWidth(line))
	return line + baseBackgroundReset() + strings.Repeat(" ", pad)
}

func stripTrailingSGR(s string) string {
	if !strings.HasSuffix(s, "m") {
		return s
	}
	idx := strings.LastIndex(s, "\x1b[")
	if idx < 0 {
		return s
	}
	return s[:idx]
}

func (m Model) renderTextSlot(content string, width, height, padX, padY int) string {
	innerWidth := max(1, width-(padX*2))
	innerHeight := max(1, height-(padY*2))
	text := m.renderFixedBlock(content, innerWidth, innerHeight)
	lines := strings.Split(text, "\n")
	out := make([]string, 0, height)
	for i := 0; i < padY; i++ {
		out = append(out, strings.Repeat(" ", width))
	}
	for _, line := range lines {
		out = append(out, strings.Repeat(" ", padX)+fitToWidth(line, innerWidth)+strings.Repeat(" ", padX))
	}
	for len(out) < height {
		out = append(out, strings.Repeat(" ", width))
	}
	return strings.Join(out, "\n")
}

func defaultArtworkANSI() string {
	lines := []string{
		"  +--------+  ",
		"  |  /\\    |  ",
		"  | /  \\   |  ",
		"  | \\__/ o |  ",
		"  |        |  ",
		"  +--------+  ",
		"    NO ART    ",
	}
	for i, line := range lines {
		lines[i] = paint(fitToWidth(line, 14), activePalette.TextMuted, "")
	}
	return strings.Join(lines, "\n")
}

func bootstrapCmd(loader Loader) tea.Cmd {
	if loader == nil {
		return nil
	}
	return func() tea.Msg {
		data, err := loader.Bootstrap(context.Background())
		if err != nil {
			return loadFailedMsg{message: fmt.Sprintf("Bootstrap error: %v", err)}
		}
		return bootstrapLoadedMsg{playlists: data.Playlists}
	}
}

func startPlaybackCmdWithEvents(playID int, trackID string, runtime PlayerRuntime, quality deezer.AudioQuality, seekMS uint64, events chan tea.Msg) tea.Cmd {
	if runtime == nil {
		return nil
	}
	return func() tea.Msg {
		handler := player.EventHandler{
			OnTrackChanged: func(meta deezer.TrackMetadata, q deezer.AudioQuality, initialMS uint64) {
				events <- playbackTrackChangedMsg{playID: playID, meta: meta, quality: q, initialMS: initialMS}
			},
			OnBufferingProgress: func(percent uint8, stage player.BufferingStage) {
				events <- bufferingProgressMsg{playID: playID, percent: percent, stage: stage}
			},
			OnPlaybackProgress: func(currentMS, totalMS uint64) {
				events <- playbackProgressMsg{playID: playID, currentMS: currentMS, totalMS: totalMS}
			},
			OnAudioBands: func(bands []uint8) {
				copied := append([]uint8(nil), bands...)
				select {
				case events <- playbackVisualizerMsg{playID: playID, bands: copied}:
				default:
				}
			},
			OnError: func(err error) {
				events <- playbackErrorMsg{playID: playID, err: err}
			},
		}
		session, err := runtime.Start(trackID, quality, seekMS, handler)
		if err != nil {
			return playbackStartFailedMsg{playID: playID, trackID: trackID, quality: quality, err: err}
		}
		return playbackStartedMsg{playID: playID, session: session}
	}
}

func waitPlaybackCmd(playID int, session PlaybackSession) tea.Cmd {
	if session == nil {
		return nil
	}
	return func() tea.Msg {
		return playbackFinishedMsg{playID: playID, err: session.Wait()}
	}
}

func listenMediaControlCmd(events <-chan MediaControlCommand) tea.Cmd {
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		command, ok := <-events
		if !ok {
			return nil
		}
		return mediaControlCommandMsg{command: command}
	}
}

func listenPlaybackEventCmd(events <-chan tea.Msg) tea.Cmd {
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		return <-events
	}
}

func playbackTickCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		return playbackTickMsg{}
	})
}

func loadingTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return loadingTickMsg{}
	})
}

func prebufferTickCmd() tea.Cmd {
	return tea.Tick(350*time.Millisecond, func(time.Time) tea.Msg {
		return prebufferTickMsg{}
	})
}

func loadHomeCmd(loader Loader) tea.Cmd {
	if loader == nil {
		return nil
	}
	return func() tea.Msg {
		tracks, err := loader.LoadHome(context.Background())
		if err != nil {
			return loadFailedMsg{message: fmt.Sprintf("Home error: %v", err)}
		}
		return collectionLoadedMsg{id: "__home__", title: "Home", tracks: tracks}
	}
}

func loadFlowCmd(loader Loader, index int, append bool, autoplay bool) tea.Cmd {
	if loader == nil {
		return nil
	}
	return func() tea.Msg {
		tracks, err := loader.LoadFlow(context.Background(), index)
		if err != nil {
			return loadFailedMsg{message: fmt.Sprintf("Flow error: %v", err)}
		}
		return collectionLoadedMsg{id: "__flow__", title: "Flow", tracks: tracks, isFlow: true, append: append, autoplay: autoplay}
	}
}

func loadExploreCmd(loader Loader) tea.Cmd {
	if loader == nil {
		return nil
	}
	return func() tea.Msg {
		tracks, err := loader.LoadExplore(context.Background())
		if err != nil {
			return loadFailedMsg{message: fmt.Sprintf("Explore error: %v", err)}
		}
		return collectionLoadedMsg{id: "__explore__", title: "Explore", tracks: tracks}
	}
}

func loadFavoritesCmd(loader Loader) tea.Cmd {
	if loader == nil {
		return nil
	}
	return func() tea.Msg {
		tracks, err := loader.LoadFavorites(context.Background())
		if err != nil {
			return loadFailedMsg{message: fmt.Sprintf("Favorites error: %v", err)}
		}
		return collectionLoadedMsg{id: "__favorites__", title: "Favorites", tracks: tracks}
	}
}

func loadPlaylistCmd(loader Loader, id string, title string) tea.Cmd {
	if loader == nil {
		return nil
	}
	return func() tea.Msg {
		tracks, err := loader.LoadPlaylist(context.Background(), id)
		if err != nil {
			return loadFailedMsg{message: fmt.Sprintf("Playlist error: %v", err)}
		}
		return collectionLoadedMsg{id: id, title: title, tracks: tracks}
	}
}

func searchCmd(loader Loader, requestID int, query string) tea.Cmd {
	if loader == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
		defer cancel()
		results, err := loader.Search(ctx, query)
		if err != nil {
			return searchFailedMsg{requestID: requestID, message: fmt.Sprintf("Search error: %v", err)}
		}
		return searchLoadedMsg{
			requestID: requestID,
			query:     query,
			tracks:    results.Tracks,
			playlists: results.Playlists,
			artists:   results.Artists,
		}
	}
}

func repeatModeLabel(mode app.RepeatMode) string {
	switch mode {
	case app.RepeatModeAll:
		return "All"
	case app.RepeatModeOne:
		return "One"
	default:
		return "Off"
	}
}

func nextRepeatMode(mode app.RepeatMode) app.RepeatMode {
	switch mode {
	case app.RepeatModeOff:
		return app.RepeatModeAll
	case app.RepeatModeAll:
		return app.RepeatModeOne
	default:
		return app.RepeatModeOff
	}
}

func nextDeezerQuality(current deezer.AudioQuality, direction int) deezer.AudioQuality {
	qualities := []deezer.AudioQuality{deezer.AudioQuality128, deezer.AudioQuality320, deezer.AudioQualityFlac}
	idx := 1
	for i, quality := range qualities {
		if quality == current {
			idx = i
			break
		}
	}
	if direction < 0 {
		idx--
		if idx < 0 {
			idx = 0
		}
	} else if direction > 0 {
		idx = min(idx+1, len(qualities)-1)
	}
	return qualities[idx]
}

func qualityFromConfig(q config.AudioQuality) deezer.AudioQuality {
	switch q {
	case config.AudioQuality128:
		return deezer.AudioQuality128
	case config.AudioQualityFlac:
		return deezer.AudioQualityFlac
	default:
		return deezer.AudioQuality320
	}
}

func configQualityFromDeezer(q deezer.AudioQuality) config.AudioQuality {
	switch q {
	case deezer.AudioQuality128:
		return config.AudioQuality128
	case deezer.AudioQualityFlac:
		return config.AudioQualityFlac
	default:
		return config.AudioQuality320
	}
}

func ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

func intPtr(v int) *int { return &v }

func stringPtr(v string) *string { return &v }

func derefOrZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func max0(v int) int {
	return max(v, 0)
}

func scrollStart(index, total, visible, following int) int {
	if total <= 0 || visible <= 0 || visible >= total || index < 0 {
		return 0
	}
	index = min(max(index, 0), total-1)
	following = max(following, 0)
	start := index + following - visible + 1
	start = max(start, 0)
	return min(start, total-visible)
}

func joinColumns(columns ...string) string {
	split := make([][]string, len(columns))
	maxLines := 0
	widths := make([]int, len(columns))
	for i, col := range columns {
		lines := strings.Split(col, "\n")
		split[i] = lines
		if len(lines) > maxLines {
			maxLines = len(lines)
		}
		for _, line := range lines {
			if textWidth(line) > widths[i] {
				widths[i] = textWidth(line)
			}
		}
	}

	rows := make([]string, 0, maxLines)
	for row := 0; row < maxLines; row++ {
		parts := make([]string, 0, len(columns))
		for col := range columns {
			line := ""
			if row < len(split[col]) {
				line = split[col][row]
			}
			parts = append(parts, padRight(line, widths[col]))
		}
		rows = append(rows, strings.Join(parts, "  "))
	}
	return strings.Join(rows, "\n")
}

func formatQueue(tracks []app.Track) []string {
	queue := make([]string, 0, len(tracks))
	for _, track := range tracks {
		queue = append(queue, track.Title+" - "+track.Artist)
	}
	return queue
}

type searchTrackColumnWidths struct {
	title  int
	artist int
	album  int
}

func searchTrackColumns(contentWidth int) searchTrackColumnWidths {
	available := max(30, contentWidth-12)
	title := max(12, available*45/100)
	artist := max(8, available*25/100)
	album := max(8, available-title-artist)
	if total := title + artist + album; total > available {
		overflow := total - available
		for overflow > 0 && title > 12 {
			title--
			overflow--
		}
		for overflow > 0 && album > 8 {
			album--
			overflow--
		}
		for overflow > 0 && artist > 8 {
			artist--
			overflow--
		}
	}
	return searchTrackColumnWidths{title: title, artist: artist, album: album}
}

func formatSearchTrackHeader(widths searchTrackColumnWidths) string {
	return fmt.Sprintf(" #  %-*s %-*s %-*s Year", widths.title, "Title", widths.artist, "Artist", widths.album, "Album")
}

func formatSearchTrackRow(index int, track app.Track, widths searchTrackColumnWidths) string {
	return fmt.Sprintf(" %02d %-*s %-*s %-*s %s",
		index,
		widths.title,
		truncate(track.Title, widths.title),
		widths.artist,
		truncate(track.Artist, widths.artist),
		widths.album,
		truncate(emptyFallback(track.Album, "-"), widths.album),
		truncate(emptyFallback(track.Year, "-"), 4),
	)
}

func emptyFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func truncate(s string, width int) string {
	if width <= 0 || textWidth(s) <= width {
		return s
	}
	if width <= 3 {
		return cellansi.Truncate(s, width, "")
	}
	return cellansi.Truncate(s, width, "...")
}

func renderProgress(currentMS, totalMS uint64, width int) string {
	if width < 4 {
		width = 4
	}
	filled := 0
	if totalMS > 0 {
		filled = int((currentMS * uint64(width)) / totalMS)
		if filled > width {
			filled = width
		}
	}
	filledBar := paint(strings.Repeat("━", filled), activePalette.Orange, "")
	emptyBar := paint(strings.Repeat("-", width-filled), activePalette.Border, "")
	timing := paint(fmt.Sprintf("%s / %s", formatClock(currentMS), formatClock(totalMS)), activePalette.TextMuted, "")
	return fmt.Sprintf("[%s%s] %s", filledBar, emptyBar, timing)
}

func (m Model) renderDisplayBody(width, height int) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	lines := make([]string, height)
	mode := displayMode(m.app.Config)
	if mode == config.DisplayModeOff || len(m.visualizerBands) == 0 {
		for i := range lines {
			lines[i] = strings.Repeat(" ", width)
		}
		return strings.Join(lines, "\n")
	}

	return renderEqualizerDisplay(width, height, m.visualizerBands, m.visualizerPeaks)
}

func renderEqualizerDisplay(width, height int, bands []uint8, peaks []float64) string {
	grid := blankDisplayGrid(width, height)
	if len(bands) == 0 {
		return displayGridString(grid)
	}
	bars := max(len(bands)*3, min(width, len(bands)*5))
	gap := 1
	barWidth := max(1, (width-(bars-1)*gap)/bars)
	usedWidth := bars*barWidth + (bars-1)*gap
	if usedWidth > width {
		gap = 0
		barWidth = max(1, width/bars)
		usedWidth = bars * barWidth
	}
	leftPad := max(0, (width-usedWidth)/2)
	for bar := 0; bar < bars; bar++ {
		position := float64(bar) * float64(len(bands)-1) / math.Max(1, float64(bars-1))
		level := interpolatedBandLevel(bands, position)
		fill := int(math.Round(level * float64(height)))
		peakLevel := interpolatedPeakLevel(peaks, position)
		peakRow := height - 1 - int(math.Round(peakLevel*float64(height-1)))
		x0 := leftPad + bar*(barWidth+gap)
		for y := height - 1; y >= max(0, height-fill); y-- {
			for x := x0; x < min(width, x0+barWidth); x++ {
				grid[y][x] = displayCell{
					glyph: equalizerGlyph(level, y, height),
					color: equalizerColor(level, y, height),
				}
			}
		}
		if peakLevel > 0 && peakRow >= 0 && peakRow < height {
			for x := x0; x < min(width, x0+barWidth); x++ {
				grid[peakRow][x] = displayCell{glyph: "▀", color: activePalette.TextStrong}
			}
		}
	}
	return displayGridString(grid)
}

func equalizerGlyph(level float64, row, height int) string {
	switch {
	case level > 0.72 && row < height/3:
		return "█"
	case level > 0.42:
		return "▓"
	default:
		return "▒"
	}
}

func equalizerColor(level float64, row, height int) string {
	switch {
	case level > 0.78 && row < height/3:
		return activePalette.Orange
	case level > 0.55:
		return activePalette.Yellow
	case level > 0.32:
		return activePalette.Aqua
	default:
		return activePalette.Green
	}
}

func interpolatedBandLevel(bands []uint8, position float64) float64 {
	if len(bands) == 0 {
		return 0
	}
	if len(bands) == 1 {
		return float64(bands[0]) / 8
	}
	if position < 0 {
		position = 0
	}
	maxPosition := float64(len(bands) - 1)
	if position > maxPosition {
		position = maxPosition
	}
	left := int(math.Floor(position))
	right := min(len(bands)-1, left+1)
	weight := position - float64(left)
	value := float64(bands[left])*(1-weight) + float64(bands[right])*weight
	return value / 8
}

func interpolatedPeakLevel(peaks []float64, position float64) float64 {
	if len(peaks) == 0 {
		return 0
	}
	if len(peaks) == 1 {
		return clampFloat64(peaks[0], 0, 1)
	}
	if position < 0 {
		position = 0
	}
	maxPosition := float64(len(peaks) - 1)
	if position > maxPosition {
		position = maxPosition
	}
	left := int(math.Floor(position))
	right := min(len(peaks)-1, left+1)
	weight := position - float64(left)
	value := peaks[left]*(1-weight) + peaks[right]*weight
	return clampFloat64(value, 0, 1)
}

func clampFloat64(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

type displayCell struct {
	glyph string
	color string
}

func blankDisplayGrid(width, height int) [][]displayCell {
	grid := make([][]displayCell, height)
	for row := range grid {
		grid[row] = make([]displayCell, width)
	}
	return grid
}

func displayGridString(grid [][]displayCell) string {
	out := make([]string, len(grid))
	for row := range grid {
		var line strings.Builder
		line.Grow(len(grid[row]))
		for _, cell := range grid[row] {
			if cell.glyph == "" {
				line.WriteByte(' ')
				continue
			}
			line.WriteString(paint(cell.glyph, cell.color, ""))
		}
		out[row] = line.String()
	}
	return strings.Join(out, "\n")
}

func displayMode(cfg config.Config) config.DisplayMode {
	return config.NormalizeDisplayMode(cfg.DisplayMode, cfg.DisplayEnabled)
}

func formatClock(ms uint64) string {
	seconds := ms / 1000
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}

func bufferingStatusMessage(percent int, stage player.BufferingStage) string {
	if strings.TrimSpace(string(stage)) == "" {
		return fmt.Sprintf("Buffering %d%%", percent)
	}
	return fmt.Sprintf("Buffering %d%% - %s", percent, stage)
}

func renderSearchTabs(category app.SearchCategory) string {
	tabs := []struct {
		label string
		value app.SearchCategory
	}{
		{label: "Tracks", value: app.SearchCategoryTracks},
		{label: "Playlists", value: app.SearchCategoryPlaylists},
		{label: "Artists", value: app.SearchCategoryArtists},
	}
	parts := make([]string, 0, len(tabs))
	for _, tab := range tabs {
		label := fmt.Sprintf(" %-9s ", strings.ToUpper(tab.label))
		if tab.value == category {
			label = paint(label, activePalette.Orange, "")
		} else {
			label = paint(label, activePalette.TextMuted, "")
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, " | ")
}

func renderLineBox(left, right string, width int) string {
	inner := max(10, width-2)
	left = truncate(left, inner)
	right = truncate(right, inner/2)
	space := inner - textWidth(left) - textWidth(right)
	if space < 1 {
		right = ""
		space = inner - textWidth(left)
	}
	border := paint("┌"+strings.Repeat("─", inner)+"┐", activePalette.Border, activePalette.Background)
	content := paint("│", activePalette.Border, activePalette.Background) +
		paint(fitToWidth(left+strings.Repeat(" ", max(0, space))+right, inner), activePalette.Text, activePalette.Background) +
		paint("│", activePalette.Border, activePalette.Background)
	bottom := paint("└"+strings.Repeat("─", inner)+"┘", activePalette.Border, activePalette.Background)
	return border + "\n" + content + "\n" + bottom
}

func renderTripleLine(left, center, right string, width int) string {
	inner := max(10, width-2)
	left = truncate(left, inner/3)
	right = truncate(right, inner/3)
	remaining := inner - textWidth(left) - textWidth(right)
	center = truncate(center, remaining)
	leftPad := max(0, (remaining-textWidth(center))/2)
	rightPad := max(0, remaining-textWidth(center)-leftPad)
	top := paint("┌"+strings.Repeat("─", inner)+"┐", activePalette.Border, activePalette.Background)
	content := paint("│", activePalette.Border, activePalette.Background) +
		paint(fitToWidth(
			paint(left, activePalette.Orange, "")+
				strings.Repeat(" ", leftPad)+
				paint(center, activePalette.TextMuted, "")+
				strings.Repeat(" ", rightPad)+
				paint(right, activePalette.TextMuted, ""),
			inner,
		), activePalette.Text, activePalette.Background) +
		paint("│", activePalette.Border, activePalette.Background)
	bottom := paint("└"+strings.Repeat("─", inner)+"┘", activePalette.Border, activePalette.Background)
	return top + "\n" + content + "\n" + bottom
}

func displayCollectionTitle(id string) string {
	switch id {
	case "__home__":
		return "Home"
	case "__flow__":
		return "Flow"
	case "__explore__":
		return "Explore"
	case "__favorites__":
		return "Favorites"
	case "__search__":
		return "Search"
	case "":
		return "Browse"
	default:
		return id
	}
}

func derefString(v *string, fallback string) string {
	if v == nil {
		return fallback
	}
	return *v
}

func padRight(s string, width int) string {
	if textWidth(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-textWidth(s))
}

func fitToWidth(s string, width int) string {
	return padRight(truncate(s, width), width)
}

func fillBackground(content string, width int) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = paint(fitToWidth(line, width), "", activePalette.Background)
	}
	return strings.Join(lines, "\n")
}

func textWidth(s string) int {
	return cellansi.StringWidth(s)
}

func activePanelLabel(panel app.ActivePanel) string {
	switch panel {
	case app.ActivePanelNavigation:
		return "nav"
	case app.ActivePanelPlaylists:
		return "playlists"
	case app.ActivePanelSearch:
		return "search"
	case app.ActivePanelMain:
		return "tracks"
	case app.ActivePanelQueue:
		return "queue"
	case app.ActivePanelPlayer, app.ActivePanelPlayerInfo, app.ActivePanelPlayerProgress:
		return "player"
	default:
		return "app"
	}
}

func qualityLabel(q config.AudioQuality) string {
	switch q {
	case config.AudioQuality128:
		return "128 kbps"
	case config.AudioQualityFlac:
		return "FLAC"
	default:
		return "320 kbps"
	}
}

func applyTheme(theme colorscheme.Name) {
	activePalette = colorscheme.Lookup(theme).Palette
}

func nextQuality(current config.AudioQuality, direction int) config.AudioQuality {
	qualities := []config.AudioQuality{config.AudioQuality128, config.AudioQuality320, config.AudioQualityFlac}
	idx := 1
	for i, quality := range qualities {
		if quality == current {
			idx = i
			break
		}
	}
	if direction < 0 {
		idx = (idx - 1 + len(qualities)) % len(qualities)
	} else {
		idx = (idx + 1) % len(qualities)
	}
	return qualities[idx]
}

func nextCrossfadeDuration(current uint64, direction int) uint64 {
	presets := []uint64{0, 1000, 3000, 5000, 8000, 10000, 13000}
	idx := 0
	for i, value := range presets {
		if value == current {
			idx = i
			break
		}
		if value < current {
			idx = i
		}
	}
	if direction < 0 {
		idx = (idx - 1 + len(presets)) % len(presets)
	} else {
		idx = (idx + 1) % len(presets)
	}
	return presets[idx]
}

func nextDisplayMode(current config.DisplayMode, direction int) config.DisplayMode {
	modes := []config.DisplayMode{
		config.DisplayModeOff,
		config.DisplayModeEqualizer,
	}
	current = config.NormalizeDisplayMode(current, true)
	idx := 1
	for i, mode := range modes {
		if mode == current {
			idx = i
			break
		}
	}
	if direction < 0 {
		idx = (idx - 1 + len(modes)) % len(modes)
	} else {
		idx = (idx + 1) % len(modes)
	}
	return modes[idx]
}

func displayModeLabel(mode config.DisplayMode) string {
	switch config.NormalizeDisplayMode(mode, true) {
	case config.DisplayModeOff:
		return "Off"
	default:
		return "Equalizer"
	}
}

func onOff(v bool) string {
	if v {
		return "On"
	}
	return "Off"
}

func (m Model) renderSettingsRows() []string {
	selected := derefOrZero(m.app.SettingsState.Selected())
	rows := []struct {
		label string
		value string
		color string
	}{
		{label: "Theme", value: colorscheme.Label(m.app.Config.Theme), color: activePalette.Blue},
		{label: "Volume", value: fmt.Sprintf("%d%%", m.app.Volume), color: activePalette.Aqua},
		{label: "Quality", value: qualityLabel(m.app.Config.DefaultQuality), color: activePalette.Purple},
		{label: "Crossfade", value: onOff(m.app.Config.CrossfadeEnabled), color: activePalette.Orange},
		{label: "Duration", value: fmt.Sprintf("%dms", m.app.Config.CrossfadeDurationMS), color: activePalette.Yellow},
		{label: "Display", value: displayModeLabel(m.app.Config.DisplayMode), color: activePalette.Aqua},
	}
	lines := make([]string, 0, len(rows))
	for i, row := range rows {
		line := kvLine(row.label, row.value, row.color)
		if i == selected {
			line = trackRow(line, true, row.color)
		}
		lines = append(lines, line)
	}
	return lines
}

var loadingHeartFrames = [][]string{
	{
		"    ██    ██    ",
		"  ████████████  ",
		" ██████████████ ",
		"  ████████████  ",
		"   ██████████   ",
		"     ██████     ",
		"       ██       ",
	},
	{
		"   ████  ████   ",
		" ██████████████ ",
		"████████████████",
		" ██████████████ ",
		"  ████████████  ",
		"    ████████    ",
		"      ████      ",
	},
	{
		"  ██████  ██████  ",
		" ████████████████ ",
		"██████████████████",
		" ████████████████ ",
		"  ██████████████  ",
		"    ██████████    ",
		"       ████       ",
	},
	{
		"   ████  ████   ",
		" ██████████████ ",
		"████████████████",
		" ██████████████ ",
		"  ████████████  ",
		"    ████████    ",
		"      ████      ",
	},
}

var loadingWordmark = []string{
	"▄ ▄▖▄▖▄▖▄▖▄▖",
	"▌▌▙▖▙▖▗▘▙▖▙▘",
	"▙▘▙▖▙▖▙▖▙▖▌▌",
}

func (m Model) renderLoadingScreen() string {
	frame := loadingHeartFrames[m.loadingFrame%len(loadingHeartFrames)]
	lines := make([]string, 0, len(frame)+len(loadingWordmark)+3)
	for _, line := range frame {
		lines = append(lines, centerText(paint(line, activePalette.Purple, ""), m.width))
	}
	lines = append(lines, "")
	for _, line := range loadingWordmark {
		lines = append(lines, centerText(paint(line, activePalette.Aqua, ""), m.width))
	}
	lines = append(lines, centerText(paint(strings.TrimSpace(m.app.StatusMessage), activePalette.TextMuted, ""), m.width))
	return verticalCenter(strings.Join(lines, "\n"), m.height)
}

func centerText(text string, width int) string {
	text = truncate(text, max(1, width))
	pad := max(0, (width-textWidth(text))/2)
	return strings.Repeat(" ", pad) + text
}

func verticalCenter(content string, height int) string {
	lines := strings.Split(content, "\n")
	if len(lines) >= height {
		return strings.Join(lines[:height], "\n")
	}
	topPad := max(0, (height-len(lines))/2)
	out := make([]string, 0, height)
	for i := 0; i < topPad; i++ {
		out = append(out, "")
	}
	out = append(out, lines...)
	for len(out) < height {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

func sectionHeading(text, color string) string {
	return paint(" "+text, color, "")
}

func tableHeader(text, color string) string {
	return paint(text, color, "")
}

func separatorLine(width int, color string) string {
	return paint(" "+strings.Repeat("─", max(0, width-1)), color, "")
}

func kvLine(label, value, valueColor string) string {
	return paint(fmt.Sprintf(" %-10s ", label+":"), activePalette.TextMuted, "") + paint(value, valueColor, "")
}

func listRow(text string, selected bool, accent string) string {
	if selected {
		return paint("> ", accent, "") + paint(text, activePalette.TextStrong, "")
	}
	return paint("  ", accent, "") + paint(text, activePalette.Text, "")
}

func trackRow(text string, selected bool, accent string) string {
	if selected {
		return paint("> ", accent, "") + paint(strings.TrimLeft(text, " "), activePalette.TextStrong, "")
	}
	return "  " + paint(strings.TrimLeft(text, " "), activePalette.Text, "")
}

func paint(text, fg, bg string) string {
	if text == "" {
		return ""
	}
	var parts []string
	var resets []string
	if fg != "" {
		parts = append(parts, hexToANSI(fg, 38))
		resets = append(resets, "39")
	}
	if bg != "" {
		parts = append(parts, hexToANSI(bg, 48))
		resets = append(resets, "49")
	}
	if len(parts) == 0 {
		return text
	}
	resetBG := bg
	if resetBG == "" {
		resetBG = activePalette.Background
	}
	reset := append([]string{"39", hexToANSI(resetBG, 48)}, resetsWithoutColorReset(resets)...)
	return "\x1b[" + strings.Join(parts, ";") + "m" + text + "\x1b[" + strings.Join(reset, ";") + "m"
}

func baseBackgroundReset() string {
	return "\x1b[39;" + hexToANSI(activePalette.Background, 48) + "m"
}

func resetsWithoutColorReset(resets []string) []string {
	filtered := make([]string, 0, len(resets))
	for _, reset := range resets {
		if reset == "39" || reset == "49" {
			continue
		}
		filtered = append(filtered, reset)
	}
	return filtered
}

func hexToANSI(hex string, mode int) string {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return ""
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b); err != nil {
		return ""
	}
	return fmt.Sprintf("%d;2;%d;%d;%d", mode, r, g, b)
}

func (m Model) displayState() string {
	status := strings.TrimSpace(m.app.StatusMessage)
	switch {
	case strings.EqualFold(status, "Buffering..."):
		return "Buffering"
	case strings.HasPrefix(status, "Buffering "):
		return status
	case m.bufferingPercent != nil:
		return fmt.Sprintf("Buffering %d%%", *m.bufferingPercent)
	case strings.EqualFold(status, "Paused"):
		return "Paused"
	case strings.EqualFold(status, "Playing"):
		return "Playing"
	case strings.EqualFold(status, "Starting playback..."):
		return "Starting"
	case strings.EqualFold(status, "Playback finished"):
		return "Finished"
	case strings.HasPrefix(status, "Playback error:"):
		return "Playback error"
	case strings.HasPrefix(status, "Playback runtime error:"):
		return "Playback runtime error"
	case strings.EqualFold(status, "Loading more Flow..."):
		return "Loading more Flow"
	case m.pauseRequested:
		return "Paused"
	case m.app.IsPlaying:
		return "Playing"
	case m.session != nil:
		return "Ready"
	default:
		return "Stopped"
	}
}

func (m Model) selectedTrack() *app.Track {
	switch m.app.ActivePanel {
	case app.ActivePanelQueue:
		idx := derefOrZero(m.app.QueueState.Selected())
		if idx >= 0 && idx < len(m.app.QueueTracks) {
			track := m.app.QueueTracks[idx]
			return &track
		}
	case app.ActivePanelMain, app.ActivePanelSearch:
		if len(m.app.CurrentTracks) == 0 {
			return nil
		}
		idx := derefOrZero(m.app.MainState.Selected())
		if m.app.ShowingSearchResult {
			if m.app.SearchCategory != app.SearchCategoryTracks {
				return nil
			}
			if idx >= 0 && idx < len(m.app.CurrentTracks) {
				track := m.app.CurrentTracks[idx]
				return &track
			}
			return nil
		}
		if idx <= 0 {
			return nil
		}
		trackIdx := idx - 1
		if trackIdx >= 0 && trackIdx < len(m.app.CurrentTracks) {
			track := m.app.CurrentTracks[trackIdx]
			return &track
		}
	}
	return nil
}
