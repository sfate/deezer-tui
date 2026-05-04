package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	cellansi "github.com/charmbracelet/x/ansi"

	"deezer-tui-go/internal/app"
	"deezer-tui-go/internal/config"
	"deezer-tui-go/internal/deezer"
	"deezer-tui-go/internal/player"
)

var (
	gruvboxBgHard = "#100c18"
	gruvboxBg0    = "#15111f"
	gruvboxBg1    = "#1c1629"
	gruvboxBg3    = "#3d314a"
	gruvboxFg0    = "#e4d4de"
	gruvboxFg1    = "#c8b3bf"
	gruvboxFg4    = "#8f7383"
	gruvboxYellow = "#f3c969"
	gruvboxBlue   = "#8f7383"
	gruvboxAqua   = "#21c7d9"
	gruvboxGreen  = "#21c7d9"
	gruvboxOrange = "#e07a87"
	gruvboxRed    = "#e06c75"
	gruvboxPurple = "#b18bb8"
)

const seekStepMS uint64 = 10_000

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
	query     string
	tracks    []app.Track
	playlists []app.Playlist
	artists   []app.Artist
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
}

type playbackProgressMsg struct {
	playID    int
	currentMS uint64
	totalMS   uint64
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

type artworkLoadedMsg struct {
	url string
	art string
}

type loadingTickMsg struct{}

type mediaControlCommandMsg struct {
	command MediaControlCommand
}

type Model struct {
	app              *app.App
	loader           Loader
	runtime          PlayerRuntime
	session          PlaybackSession
	playbackEvents   chan tea.Msg
	progressBaseMS   uint64
	progressSince    time.Time
	progressActive   bool
	bufferingPercent *int
	pauseRequested   bool
	saveConfig       func(config.Config) error
	artworkURL       string
	artworkANSI      string
	artCache         map[string]string
	width            int
	height           int
	nextPlaybackID   int
	currentPlayID    int
	currentTrackID   string
	currentQuality   deezer.AudioQuality
	playbackRetries  int
	favoritesSortAsc bool
	ready            bool
	loadingFrame     int
}

func New() Model {
	return NewWithConfig(config.Load())
}

func NewWithConfig(cfg config.Config) Model {
	cfg.Theme = config.NormalizeTheme(cfg.Theme)
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
		app:            state,
		loader:         loader,
		runtime:        newPlayerRuntime(loader),
		playbackEvents: make(chan tea.Msg, 32),
		saveConfig:     config.Save,
		artCache:       map[string]string{},
		ready:          loader == nil,
	}
}

func NewWithLoader(cfg config.Config, loader Loader) Model {
	cfg.Theme = config.NormalizeTheme(cfg.Theme)
	applyTheme(cfg.Theme)
	state := app.New(cfg)
	state.StatusMessage = "Loading Deezer library..."
	if loader == nil {
		state.StatusMessage = "No Deezer loader configured"
	}
	return Model{
		app:            state,
		loader:         loader,
		runtime:        newPlayerRuntime(loader),
		playbackEvents: make(chan tea.Msg, 32),
		saveConfig:     config.Save,
		artCache:       map[string]string{},
		ready:          loader == nil,
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
			if oldQueueLen < len(m.app.QueueTracks) {
				m.prebufferTrack(m.app.QueueTracks[oldQueueLen].ID)
			}
			return m, nil
		}

		m.app.CurrentPlaylistID = stringPtr(msg.id)
		if msg.isFlow {
			m.app.FlowNextIndex = len(msg.tracks)
			m.app.IsFlowQueue = true
			trackID := m.app.LoadFlowTracks(msg.tracks, msg.autoplay)
			m.ready = true
			m.app.StatusMessage = fmt.Sprintf("Loaded %s (%d tracks)", msg.title, len(msg.tracks))
			if trackID != nil {
				return m, m.startTrackPlayback(*trackID)
			}
			return m, nil
		}

		m.loadCollection(msg.id, msg.title, msg.tracks)
		m.ready = true
		if msg.id == "__home__" {
			m.app.ActivePanel = app.ActivePanelNavigation
		}
		return m, nil
	case loadFailedMsg:
		m.app.StatusMessage = msg.message
		m.app.FlowLoadingMore = false
		m.app.IsSearching = false
		m.ready = true
		return m, nil
	case loadingTickMsg:
		if !m.ready {
			m.loadingFrame = (m.loadingFrame + 1) % len(loadingLogoFrames)
			return m, loadingTickCmd()
		}
		return m, nil
	case mediaControlCommandMsg:
		return m, tea.Batch(m.handleMediaControlCommand(msg.command), m.listenMediaControlCmd())
	case searchLoadedMsg:
		m.app.IsSearching = false
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
		m.app.StatusMessage = fmt.Sprintf("Buffering %d%%", percent)
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
		m.bufferingPercent = nil
		m.progressBaseMS = msg.initialMS
		m.progressSince = time.Now()
		m.progressActive = m.app.IsPlaying
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
			m.app.ViewingSettings = false
			m.app.ActivePanel = app.ActivePanelNavigation
			m.app.StatusMessage = "Library"
		case "tab":
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
		case "[":
			return m, m.switchCurrentQuality(-1)
		case "]":
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
			m.app.ActivePanel = app.ActivePanelSearch
			m.app.StatusMessage = "Search: type query and press Enter"
		}
	}

	return m, nil
}

func (m Model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		view := tea.NewView("Loading deezer-tui-go...")
		view.AltScreen = true
		view.WindowTitle = "deezer-tui-go"
		return view
	}

	if !m.ready {
		view := tea.NewView(fillBackground(m.renderLoadingScreen(), m.width))
		view.AltScreen = true
		view.WindowTitle = "deezer-tui-go"
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
	status := m.renderStatusLine()

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
	view.WindowTitle = "deezer-tui-go"
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
		if selected >= len(m.app.CurrentTracks) {
			selected = len(m.app.CurrentTracks) - 1
		}
		track := m.app.CurrentTracks[selected]
		m.app.QueueTracks = []app.Track{track}
		m.app.Queue = formatQueue(m.app.QueueTracks)
		m.app.QueueIndex = intPtr(0)
		m.app.QueueState.Select(intPtr(0))
		m.app.IsPlaying = true
		m.app.IsFlowQueue = false
		m.app.StatusMessage = fmt.Sprintf("Selected %s - %s", track.Title, track.Artist)
		return m.startTrackPlayback(track.ID)
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
		m.app.IsSearching = true
		m.app.SearchQuery = artist.Name
		m.app.StatusMessage = fmt.Sprintf("Searching for %q...", artist.Name)
		return searchCmd(m.loader, artist.Name)
	default:
		return nil
	}
}

func (m *Model) loadCollection(id, title string, tracks []app.Track) {
	m.app.CurrentPlaylistID = stringPtr(id)
	m.app.CurrentTracks = append([]app.Track(nil), tracks...)
	if id == "__favorites__" {
		m.applyFavoritesSort()
	}
	m.app.MainState.Select(intPtr(0))
	m.app.SearchPlaylists = nil
	m.app.SearchArtists = nil
	m.app.ShowingSearchResult = false
	m.app.ViewingSettings = false
	if m.ready {
		m.app.ActivePanel = app.ActivePanelMain
	}
	m.app.IsFlowQueue = false
	m.app.FlowLoadingMore = false
	m.app.FlowNextIndex = 0
	m.app.StatusMessage = fmt.Sprintf("Loaded %s (%d tracks)", title, len(tracks))
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
		m.app.StatusMessage = fmt.Sprintf("Searching for %q...", query)
		return searchCmd(m.loader, query)
	case "backspace":
		if len(m.app.SearchQuery) > 0 {
			m.app.SearchQuery = m.app.SearchQuery[:len(m.app.SearchQuery)-1]
		}
		return nil
	}
	if len(msg.Text) > 0 {
		m.app.SearchQuery += msg.Text
	}
	return nil
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
		return m.startTrackPlayback(m.app.QueueTracks[nextIndex].ID)
	}
	if m.app.ShouldLoadMoreFlow() {
		m.app.FlowLoadingMore = true
		m.app.StatusMessage = "Loading more Flow..."
		return loadFlowCmd(m.loader, m.app.FlowNextIndex, true, true)
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
	return m.startTrackPlayback(m.app.QueueTracks[prevIndex].ID)
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
		m.app.Config.Theme = nextTheme(m.app.Config.Theme, direction)
		applyTheme(m.app.Config.Theme)
		m.app.StatusMessage = fmt.Sprintf("Theme: %s", themeLabel(m.app.Config.Theme))
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
		if runtime, ok := m.runtime.(PrebufferingRuntime); ok {
			runtime.Prebuffer("", qualityFromConfig(m.app.Config.DefaultQuality))
		}
		return nil
	}

	nextIndex := *m.app.QueueIndex + 1
	if nextIndex < 0 || nextIndex >= len(m.app.QueueTracks) {
		if cmd := m.maybeLoadMoreFlowForTail(); cmd != nil {
			return cmd
		}
		if m.app.RepeatMode == app.RepeatModeAll && len(m.app.QueueTracks) > 0 {
			m.prebufferTrack(m.app.QueueTracks[0].ID)
			return nil
		}
		if m.app.RepeatMode == app.RepeatModeOne && *m.app.QueueIndex >= 0 && *m.app.QueueIndex < len(m.app.QueueTracks) {
			m.prebufferTrack(m.app.QueueTracks[*m.app.QueueIndex].ID)
			return nil
		}
		if runtime, ok := m.runtime.(PrebufferingRuntime); ok {
			runtime.Prebuffer("", qualityFromConfig(m.app.Config.DefaultQuality))
		}
		return nil
	}

	m.prebufferTrack(m.app.QueueTracks[nextIndex].ID)
	return nil
}

func (m *Model) maybeLoadMoreFlowForTail() tea.Cmd {
	if !m.app.ShouldLoadMoreFlow() {
		return nil
	}
	m.app.FlowLoadingMore = true
	m.app.StatusMessage = "Loading more Flow..."
	return loadFlowCmd(m.loader, m.app.FlowNextIndex, true, false)
}

func (m *Model) prebufferTrack(trackID string) {
	runtime, ok := m.runtime.(PrebufferingRuntime)
	if !ok {
		return
	}
	runtime.Prebuffer(trackID, qualityFromConfig(m.app.Config.DefaultQuality))
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
	nav := []string{sectionHeading("Browse", gruvboxBlue)}
	items := []string{"Home", "Flow", "Explore", "Favorites", "Settings"}
	selectedNav := derefOrZero(m.app.NavState.Selected())
	for i, item := range items {
		selected := i == selectedNav && m.app.ActivePanel == app.ActivePanelNavigation
		nav = append(nav, listRow(item, selected, gruvboxBlue))
	}
	nav = append(nav, "", sectionHeading("Library", gruvboxPurple))
	for i, pl := range m.app.Playlists {
		selected := i == derefOrZero(m.app.PlaylistState.Selected()) && m.app.ActivePanel == app.ActivePanelPlaylists
		nav = append(nav, listRow(truncate(pl.Title, 20), selected, gruvboxPurple))
		if i >= height-8 {
			break
		}
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
	} else if len(m.app.CurrentTracks) == 0 {
		lines = append(lines, "", paint(" No tracks loaded", gruvboxFg4, ""))
	} else {
		if m.app.ShowingSearchResult {
			lines = append(lines, renderSearchTabs(m.app.SearchCategory))
			lines = append(lines, "")
		}
		selected := derefOrZero(m.app.MainState.Selected())
		if m.app.ShowingSearchResult {
			switch m.app.SearchCategory {
			case app.SearchCategoryTracks:
				lines = append(lines, tableHeader(" #  Title                               Artist", gruvboxOrange))
				lines = append(lines, separatorLine(58, gruvboxBg3))
				for i, track := range m.app.CurrentTracks {
					label := fmt.Sprintf(" %02d %-35s %s", i+1, truncate(track.Title, 35), truncate(track.Artist, 18))
					lines = append(lines, trackRow(label, selected == i, gruvboxAqua))
				}
			case app.SearchCategoryPlaylists:
				lines = append(lines, tableHeader(" Playlist", gruvboxOrange))
				lines = append(lines, separatorLine(41, gruvboxBg3))
				for i, pl := range m.app.SearchPlaylists {
					label := fmt.Sprintf(" %02d %s", i+1, truncate(pl.Title, 40))
					lines = append(lines, trackRow(label, selected == i, gruvboxPurple))
				}
			case app.SearchCategoryArtists:
				lines = append(lines, tableHeader(" Artist", gruvboxOrange))
				lines = append(lines, separatorLine(41, gruvboxBg3))
				for i, artist := range m.app.SearchArtists {
					label := fmt.Sprintf(" %02d %s", i+1, truncate(artist.Name, 40))
					lines = append(lines, trackRow(label, selected == i, gruvboxGreen))
				}
			}
		} else {
			playAll := trackRow(" Play Collection", selected == 0, gruvboxYellow)
			lines = append(lines, playAll)
			if m.app.CurrentPlaylistID != nil && *m.app.CurrentPlaylistID == "__favorites__" {
				lines = append(lines, paint(fmt.Sprintf(" Sort: added date %s", ternary(m.favoritesSortAsc, "asc", "desc")), gruvboxFg4, ""))
			}
			lines = append(lines, "")
			lines = append(lines, tableHeader(" #  Title                               Artist", gruvboxOrange))
			lines = append(lines, separatorLine(58, gruvboxBg3))
			for i, track := range m.app.CurrentTracks {
				label := fmt.Sprintf(" %02d %-35s %s", i+1, truncate(track.Title, 35), truncate(track.Artist, 18))
				lines = append(lines, trackRow(label, selected == i+1, gruvboxAqua))
			}
		}
	}

	return m.renderPanel(title, strings.Join(lines, "\n"), m.app.ActivePanel == app.ActivePanelMain || m.app.ActivePanel == app.ActivePanelSearch, width, height)
}

func (m Model) renderQueue(width, height int) string {
	contentWidth := max(16, width-4)
	metaWidth := max(16, contentWidth-2)
	queueItemWidth := max(16, contentWidth-4)
	lines := []string{
		kvLine("State", ternary(m.app.IsPlaying, "Playing", "Stopped"), gruvboxGreen),
		kvLine("Volume", fmt.Sprintf("%d%%", m.app.Volume), gruvboxAqua),
		kvLine("Repeat", repeatModeLabel(m.app.RepeatMode), gruvboxOrange),
		kvLine("Flow", fmt.Sprintf("%t", m.app.IsFlowQueue), gruvboxPurple),
	}

	if m.app.QueueIndex != nil && *m.app.QueueIndex < len(m.app.QueueTracks) {
		track := m.app.QueueTracks[*m.app.QueueIndex]
		lines = append(lines, "", sectionHeading("Now Playing", gruvboxYellow), paint(" "+truncate(track.Title, metaWidth), gruvboxFg0, ""), paint(" "+truncate(track.Artist, metaWidth), gruvboxFg4, ""))
	} else {
		lines = append(lines, "", paint(" Nothing queued", gruvboxFg4, ""))
	}

	if len(m.app.Queue) > 0 {
		lines = append(lines, "", sectionHeading("Queue", gruvboxOrange))
		lines = append(lines, separatorLine(contentWidth, gruvboxBg3))
		for i, item := range m.app.Queue {
			line := fmt.Sprintf(" %02d %s", i+1, truncate(item, queueItemWidth))
			if m.app.ActivePanel == app.ActivePanelQueue && i == derefOrZero(m.app.QueueState.Selected()) {
				line = trackRow(line, true, gruvboxOrange)
			} else if m.app.QueueIndex != nil && i == *m.app.QueueIndex {
				line = paint("▶"+line[1:], gruvboxBgHard, gruvboxAqua)
			} else {
				line = paint(line, gruvboxFg1, "")
			}
			lines = append(lines, line)
			if i >= height-10 {
				break
			}
		}
	}

	return m.renderPanel("Queue", strings.Join(lines, "\n"), m.app.ActivePanel == app.ActivePanelQueue || m.app.ActivePanel == app.ActivePanelPlayer || m.app.ActivePanel == app.ActivePanelPlayerInfo || m.app.ActivePanel == app.ActivePanelPlayerProgress, width, height)
}

func (m Model) renderSearchBar() string {
	query := m.app.SearchQuery
	if query == "" && m.app.IsSearching {
		query = paint("_", gruvboxAqua, "")
	}
	label := paint(" Search ", gruvboxFg4, "")
	if m.app.IsSearching {
		label = paint(" Search ", gruvboxOrange, "")
	}
	help := paint("tab switch | hjkl move | enter select | space play/pause | / search", gruvboxFg4, "")
	return renderLineBox(fmt.Sprintf("%s %s", label, query), help, m.width)
}

func (m Model) renderPlaybar() string {
	controls := " space play/pause | n/p next prev | ,/. seek | [/] quality | r repeat | +/- volume "
	if m.app.CurrentPlaylistID != nil && *m.app.CurrentPlaylistID == "__favorites__" {
		controls = " space play/pause | n/p next prev | ,/. seek | [/] quality | r repeat | s sort | +/- volume "
	}
	left := paint(controls, gruvboxFg1, "")
	stateColor := gruvboxFg4
	if m.app.IsPlaying {
		stateColor = gruvboxOrange
	}
	right := fmt.Sprintf("%s | %s",
		paint(fmt.Sprintf("vol %d%%", m.app.Volume), gruvboxAqua, ""),
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

func (m Model) renderStatusLine() string {
	title := "Nothing playing"
	artist := "-"
	progress := renderProgress(0, 0, max(20, min(48, m.width-26)))
	source := displayCollectionTitle(derefString(m.app.CurrentPlaylistID, "Browse"))
	quality := "-"
	elapsed := "00:00"
	total := "00:00"
	queueInfo := "-"
	if m.app.NowPlaying != nil {
		title = truncate(m.app.NowPlaying.Title, max(20, m.width-22))
		artist = truncate(m.app.NowPlaying.Artist, max(20, m.width-22))
		progress = renderProgress(m.app.NowPlaying.CurrentMS, m.app.NowPlaying.TotalMS, max(20, min(48, m.width-26)))
		quality = qualityLabel(m.app.NowPlaying.Quality)
		elapsed = formatClock(m.app.NowPlaying.CurrentMS)
		total = formatClock(m.app.NowPlaying.TotalMS)
	} else if m.app.QueueIndex != nil && *m.app.QueueIndex < len(m.app.QueueTracks) {
		track := m.app.QueueTracks[*m.app.QueueIndex]
		title = truncate(track.Title, max(20, m.width-22))
		artist = truncate(track.Artist, max(20, m.width-22))
	}
	if m.app.QueueIndex != nil && len(m.app.QueueTracks) > 0 {
		queueInfo = fmt.Sprintf("%d/%d", *m.app.QueueIndex+1, len(m.app.QueueTracks))
	}
	lines := []string{
		kvLine("State", m.displayState(), gruvboxGreen),
		kvLine("Track", title, gruvboxYellow),
		kvLine("Artist", artist, gruvboxAqua),
		kvLine("Progress", progress, gruvboxFg1),
		kvLine("Elapsed", elapsed+" / "+total, gruvboxOrange),
		kvLine("Quality", quality, gruvboxPurple),
		kvLine("Source", source, gruvboxBlue),
		kvLine("Queue", queueInfo, gruvboxFg4),
	}
	art := m.artworkANSI
	if art == "" {
		art = defaultArtworkANSI()
	}
	body := joinColumns(
		" ",
		m.renderArtworkSlot(art, 16, 9),
		m.renderTextSlot(strings.Join(lines, "\n"), max(24, m.width-24), 9, 1, 1),
	)
	return m.renderPanel("Status", body, m.app.IsPlaying || m.app.NowPlaying != nil, m.width, 11)
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
	borderFG := gruvboxBg3
	titleFG := gruvboxFg4
	titleBG := gruvboxBg0
	panelBG := gruvboxBg0
	contentFG := gruvboxFg1
	if active {
		borderFG = gruvboxBg3
		titleFG = gruvboxOrange
		titleBG = gruvboxBg0
		panelBG = gruvboxBg0
		contentFG = gruvboxFg0
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
		out = append(out, paint(strings.Repeat(" ", width), "", gruvboxBg0))
	}
	for i := 0; i < 7; i++ {
		line := fitArtworkLine(lines[i], 14)
		out = append(out, paint(strings.Repeat(" ", sidePad), "", gruvboxBg0)+line+paint(strings.Repeat(" ", sidePad), "", gruvboxBg0))
	}
	for i := 0; i < bottomPad; i++ {
		out = append(out, paint(strings.Repeat(" ", width), "", gruvboxBg0))
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
		lines[i] = paint(fitToWidth(line, 14), gruvboxFg4, "")
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
			OnBufferingProgress: func(percent uint8) {
				events <- bufferingProgressMsg{playID: playID, percent: percent}
			},
			OnPlaybackProgress: func(currentMS, totalMS uint64) {
				events <- playbackProgressMsg{playID: playID, currentMS: currentMS, totalMS: totalMS}
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

func searchCmd(loader Loader, query string) tea.Cmd {
	if loader == nil {
		return nil
	}
	return func() tea.Msg {
		results, err := loader.Search(context.Background(), query)
		if err != nil {
			return loadFailedMsg{message: fmt.Sprintf("Search error: %v", err)}
		}
		return searchLoadedMsg{
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
	filledBar := paint(strings.Repeat("━", filled), gruvboxOrange, "")
	emptyBar := paint(strings.Repeat("-", width-filled), gruvboxBg3, "")
	timing := paint(fmt.Sprintf("%s / %s", formatClock(currentMS), formatClock(totalMS)), gruvboxFg4, "")
	return fmt.Sprintf("[%s%s] %s", filledBar, emptyBar, timing)
}

func formatClock(ms uint64) string {
	seconds := ms / 1000
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
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
		label := " " + strings.ToUpper(tab.label) + " "
		if tab.value == category {
			label = paint(strings.TrimSpace(label), gruvboxOrange, "")
		} else {
			label = paint(label, gruvboxFg4, "")
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
	border := paint("┌"+strings.Repeat("─", inner)+"┐", gruvboxBg3, gruvboxBg0)
	content := paint("│", gruvboxBg3, gruvboxBg0) +
		paint(fitToWidth(left+strings.Repeat(" ", max(0, space))+right, inner), gruvboxFg1, gruvboxBg0) +
		paint("│", gruvboxBg3, gruvboxBg0)
	bottom := paint("└"+strings.Repeat("─", inner)+"┘", gruvboxBg3, gruvboxBg0)
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
	top := paint("┌"+strings.Repeat("─", inner)+"┐", gruvboxBg3, gruvboxBg0)
	content := paint("│", gruvboxBg3, gruvboxBg0) +
		paint(fitToWidth(
			paint(left, gruvboxOrange, "")+
				strings.Repeat(" ", leftPad)+
				paint(center, gruvboxFg4, "")+
				strings.Repeat(" ", rightPad)+
				paint(right, gruvboxFg4, ""),
			inner,
		), gruvboxFg1, gruvboxBg0) +
		paint("│", gruvboxBg3, gruvboxBg0)
	bottom := paint("└"+strings.Repeat("─", inner)+"┘", gruvboxBg3, gruvboxBg0)
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
		lines[i] = paint(fitToWidth(line, width), "", gruvboxBg0)
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

type colorTheme struct {
	bgHard string
	bg0    string
	bg1    string
	bg3    string
	fg0    string
	fg1    string
	fg4    string
	yellow string
	blue   string
	aqua   string
	green  string
	orange string
	red    string
	purple string
}

var colorThemes = map[config.Theme]colorTheme{
	config.ThemeAetheria: {
		bgHard: "#100c18",
		bg0:    "#15111f",
		bg1:    "#1c1629",
		bg3:    "#3d314a",
		fg0:    "#e4d4de",
		fg1:    "#c8b3bf",
		fg4:    "#8f7383",
		yellow: "#f3c969",
		blue:   "#8f7383",
		aqua:   "#21c7d9",
		green:  "#21c7d9",
		orange: "#e07a87",
		red:    "#e06c75",
		purple: "#b18bb8",
	},
	config.ThemeGruvbox: {
		bgHard: "#1d2021",
		bg0:    "#282828",
		bg1:    "#3c3836",
		bg3:    "#665c54",
		fg0:    "#fbf1c7",
		fg1:    "#ebdbb2",
		fg4:    "#a89984",
		yellow: "#fabd2f",
		blue:   "#83a598",
		aqua:   "#8ec07c",
		green:  "#b8bb26",
		orange: "#fe8019",
		red:    "#fb4934",
		purple: "#d3869b",
	},
}

func applyTheme(theme config.Theme) {
	palette, ok := colorThemes[config.NormalizeTheme(theme)]
	if !ok {
		palette = colorThemes[config.ThemeAetheria]
	}
	gruvboxBgHard = palette.bgHard
	gruvboxBg0 = palette.bg0
	gruvboxBg1 = palette.bg1
	gruvboxBg3 = palette.bg3
	gruvboxFg0 = palette.fg0
	gruvboxFg1 = palette.fg1
	gruvboxFg4 = palette.fg4
	gruvboxYellow = palette.yellow
	gruvboxBlue = palette.blue
	gruvboxAqua = palette.aqua
	gruvboxGreen = palette.green
	gruvboxOrange = palette.orange
	gruvboxRed = palette.red
	gruvboxPurple = palette.purple
}

func themeLabel(theme config.Theme) string {
	switch config.NormalizeTheme(theme) {
	case config.ThemeGruvbox:
		return "Gruvbox"
	default:
		return "Aetheria"
	}
}

func nextTheme(current config.Theme, direction int) config.Theme {
	themes := []config.Theme{config.ThemeAetheria, config.ThemeGruvbox}
	current = config.NormalizeTheme(current)
	idx := 0
	for i, theme := range themes {
		if theme == current {
			idx = i
			break
		}
	}
	if direction < 0 {
		idx = (idx - 1 + len(themes)) % len(themes)
	} else {
		idx = (idx + 1) % len(themes)
	}
	return themes[idx]
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
		{label: "Theme", value: themeLabel(m.app.Config.Theme), color: gruvboxBlue},
		{label: "Volume", value: fmt.Sprintf("%d%%", m.app.Volume), color: gruvboxAqua},
		{label: "Quality", value: qualityLabel(m.app.Config.DefaultQuality), color: gruvboxPurple},
		{label: "Crossfade", value: onOff(m.app.Config.CrossfadeEnabled), color: gruvboxOrange},
		{label: "Duration", value: fmt.Sprintf("%dms", m.app.Config.CrossfadeDurationMS), color: gruvboxYellow},
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

var loadingLogoFrames = []string{
	"▁▃▄▅▄▃▁",
	"▂▄▆█▆▄▂",
	"▃▅█▇█▅▃",
	"▂▄▆█▆▄▂",
}

func (m Model) renderLoadingScreen() string {
	logo := []string{
		"██████╗ ███████╗███████╗███████╗███████╗██████╗    ████████╗██╗   ██╗██╗",
		"██╔══██╗██╔════╝██╔════╝╚══███╔╝██╔════╝██╔══██╗   ╚══██╔══╝██║   ██║██║",
		"██║  ██║█████╗  █████╗    ███╔╝ █████╗  ██████╔╝█████╗██║   ██║   ██║██║",
		"██║  ██║██╔══╝  ██╔══╝   ███╔╝  ██╔══╝  ██╔══██╗╚════╝██║   ██║   ██║██║",
		"██████╔╝███████╗███████╗███████╗███████╗██║  ██║      ██║   ╚██████╔╝██║",
		"╚═════╝ ╚══════╝╚══════╝╚══════╝╚══════╝╚═╝  ╚═╝      ╚═╝    ╚═════╝ ╚═╝",
	}

	lines := make([]string, 0, len(logo)+3)
	for _, line := range logo {
		lines = append(lines, centerText(paint(line, gruvboxAqua, ""), m.width))
	}
	lines = append(lines, "")
	lines = append(lines, centerText(paint(loadingLogoFrames[m.loadingFrame], gruvboxOrange, ""), m.width))
	lines = append(lines, centerText(paint(strings.TrimSpace(m.app.StatusMessage), gruvboxFg4, ""), m.width))
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
	return paint(fmt.Sprintf(" %-10s ", label+":"), gruvboxFg4, "") + paint(value, valueColor, "")
}

func listRow(text string, selected bool, accent string) string {
	if selected {
		return paint("> ", accent, "") + paint(text, gruvboxFg0, "")
	}
	return paint("  ", accent, "") + paint(text, gruvboxFg1, "")
}

func trackRow(text string, selected bool, accent string) string {
	if selected {
		return paint("> ", accent, "") + paint(strings.TrimLeft(text, " "), gruvboxFg0, "")
	}
	return "  " + paint(strings.TrimLeft(text, " "), gruvboxFg1, "")
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
		resetBG = gruvboxBg0
	}
	reset := append([]string{"39", hexToANSI(resetBG, 48)}, resetsWithoutColorReset(resets)...)
	return "\x1b[" + strings.Join(parts, ";") + "m" + text + "\x1b[" + strings.Join(reset, ";") + "m"
}

func baseBackgroundReset() string {
	return "\x1b[39;" + hexToANSI(gruvboxBg0, 48) + "m"
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
	case m.bufferingPercent != nil:
		return fmt.Sprintf("Buffering %d%%", *m.bufferingPercent)
	case strings.EqualFold(status, "Buffering..."):
		return "Buffering"
	case strings.HasPrefix(status, "Buffering "):
		return status
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
