package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	cellansi "github.com/charmbracelet/x/ansi"

	"deezer-tui-go/internal/app"
	"deezer-tui-go/internal/config"
	"deezer-tui-go/internal/deezer"
	"deezer-tui-go/internal/player"
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
	query     string
	tracks    []app.Track
	playlists []app.Playlist
	artists   []app.Artist
}

type playbackStartedMsg struct {
	playID  int
	session PlaybackSession
}

type playbackTrackChangedMsg struct {
	playID    int
	meta      deezer.TrackMetadata
	quality   deezer.AudioQuality
	initialMS uint64
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

type Model struct {
	app            *app.App
	loader         Loader
	runtime        PlayerRuntime
	session        PlaybackSession
	playbackEvents chan tea.Msg
	progressBaseMS uint64
	progressSince  time.Time
	progressActive bool
	pauseRequested bool
	artworkURL     string
	artworkANSI    string
	artCache       map[string]string
	width          int
	height         int
	nextPlaybackID int
	currentPlayID  int
}

func New() Model {
	return NewWithConfig(config.Load())
}

func NewWithConfig(cfg config.Config) Model {
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
		artCache:       map[string]string{},
	}
}

func NewWithLoader(cfg config.Config, loader Loader) Model {
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
	}
}

func NewWithLoaderAndRuntime(cfg config.Config, loader Loader, runtime PlayerRuntime) Model {
	model := NewWithLoader(cfg, loader)
	model.runtime = runtime
	return model
}

func (m Model) Init() tea.Cmd {
	if m.loader == nil {
		return nil
	}
	return bootstrapCmd(m.loader)
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
			result := m.app.AppendFlowTracks(msg.tracks, msg.autoplay)
			if result.AppendedCount == 0 {
				m.app.IsPlaying = false
				m.app.StatusMessage = "Flow returned no new tracks"
				return m, nil
			}
			m.app.FlowNextIndex += len(msg.tracks)
			m.app.StatusMessage = fmt.Sprintf("Appended %d Flow tracks", result.AppendedCount)
			if result.AutoplayTrackID != nil {
				return m, m.startTrackPlayback(*result.AutoplayTrackID)
			}
			return m, nil
		}

		m.app.CurrentPlaylistID = stringPtr(msg.id)
		if msg.isFlow {
			m.app.FlowNextIndex = len(msg.tracks)
			m.app.IsFlowQueue = true
			trackID := m.app.LoadFlowTracks(msg.tracks, msg.autoplay)
			m.app.StatusMessage = fmt.Sprintf("Loaded %s (%d tracks)", msg.title, len(msg.tracks))
			if trackID != nil {
				return m, m.startTrackPlayback(*trackID)
			}
			return m, nil
		}

		m.loadCollection(msg.id, msg.title, msg.tracks)
		return m, nil
	case loadFailedMsg:
		m.app.StatusMessage = msg.message
		m.app.FlowLoadingMore = false
		m.app.IsSearching = false
		return m, nil
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
		m.progressActive = false
		m.progressBaseMS = 0
		m.progressSince = time.Time{}
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
	case playbackTrackChangedMsg:
		if msg.playID != m.currentPlayID && msg.playID != m.nextPlaybackID {
			return m, nil
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
		m.artworkANSI = ""
		m.artworkURL = ""
		if msg.meta.AlbumArtURL != nil && strings.TrimSpace(*msg.meta.AlbumArtURL) != "" {
			m.artworkURL = *msg.meta.AlbumArtURL
			if cached, ok := m.artCache[m.artworkURL]; ok {
				m.artworkANSI = cached
				return m, listenPlaybackEventCmd(m.playbackEvents)
			}
			return m, tea.Batch(
				listenPlaybackEventCmd(m.playbackEvents),
				fetchArtworkCmd(m.artworkURL, 14, 14),
			)
		}
		return m, listenPlaybackEventCmd(m.playbackEvents)
	case playbackProgressMsg:
		if msg.playID != m.currentPlayID || m.app.NowPlaying == nil {
			return m, listenPlaybackEventCmd(m.playbackEvents)
		}
		m.app.NowPlaying.CurrentMS = msg.currentMS
		if msg.totalMS > 0 {
			m.app.NowPlaying.TotalMS = msg.totalMS
		}
		m.progressBaseMS = msg.currentMS
		m.progressSince = time.Now()
		m.progressActive = m.app.IsPlaying
		m.app.StatusMessage = "Playing"
		if m.progressActive {
			return m, tea.Batch(listenPlaybackEventCmd(m.playbackEvents), playbackTickCmd())
		}
		return m, listenPlaybackEventCmd(m.playbackEvents)
	case playbackErrorMsg:
		if msg.playID != m.currentPlayID && msg.playID != m.nextPlaybackID {
			return m, nil
		}
		m.app.StatusMessage = fmt.Sprintf("Playback runtime error: %v", msg.err)
		return m, listenPlaybackEventCmd(m.playbackEvents)
	case playbackFinishedMsg:
		if msg.playID != m.currentPlayID {
			return m, nil
		}
		m.session = nil
		m.progressActive = false
		m.progressSince = time.Time{}
		if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
			m.app.IsPlaying = false
			m.app.StatusMessage = fmt.Sprintf("Playback error: %v", msg.err)
			return m, nil
		}
		if errors.Is(msg.err, context.Canceled) {
			return m, nil
		}
		return m, m.handlePlaybackFinished()
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
		case "tab":
			m.cyclePanelForward()
		case "shift+tab":
			m.cyclePanelBackward()
		case "up", "k":
			m.app.HandleUp()
		case "down", "j":
			m.app.HandleDown()
		case "left", "h":
			m.app.HandleLeft()
		case "right", "l":
			m.app.HandleRight()
		case "enter":
			return m, m.handleEnter()
		case " ", "space":
			return m, m.handleSpacebar()
		case "n":
			return m, m.handleNext()
		case "p":
			return m, m.handlePrevious()
		case "+":
			m.adjustVolume(5)
		case "-":
			m.adjustVolume(-5)
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

	header := m.renderHeader()
	searchBar := m.renderSearchBar()
	contentHeight := max(10, m.height-20)
	body := joinColumns(
		m.renderSidebar(contentHeight),
		m.renderMain(contentHeight),
		m.renderQueue(contentHeight),
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
		m.app.ActivePanel = app.ActivePanelSearch
	case app.ActivePanelSearch:
		m.app.ActivePanel = app.ActivePanelMain
	case app.ActivePanelMain:
		m.app.ActivePanel = app.ActivePanelPlayer
	default:
		m.app.ActivePanel = app.ActivePanelNavigation
	}
}

func (m *Model) cyclePanelBackward() {
	switch m.app.ActivePanel {
	case app.ActivePanelPlayer, app.ActivePanelPlayerInfo, app.ActivePanelPlayerProgress:
		m.app.ActivePanel = app.ActivePanelMain
	case app.ActivePanelMain:
		m.app.ActivePanel = app.ActivePanelSearch
	case app.ActivePanelSearch:
		m.app.ActivePanel = app.ActivePanelQueue
	case app.ActivePanelQueue:
		m.app.ActivePanel = app.ActivePanelPlaylists
	case app.ActivePanelPlaylists:
		m.app.ActivePanel = app.ActivePanelNavigation
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
		if m.progressActive {
			return
		}
		return
	}
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
			m.app.StatusMessage = "Settings view is read-only in the Go rewrite"
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
			m.app.StatusMessage = "Settings actions not wired yet"
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
	m.app.MainState.Select(intPtr(0))
	m.app.SearchPlaylists = nil
	m.app.SearchArtists = nil
	m.app.ShowingSearchResult = false
	m.app.ViewingSettings = false
	m.app.ActivePanel = app.ActivePanelMain
	m.app.IsFlowQueue = false
	m.app.FlowLoadingMore = false
	m.app.FlowNextIndex = 0
	m.app.StatusMessage = fmt.Sprintf("Loaded %s (%d tracks)", title, len(tracks))
}

func (m *Model) handleSearchInput(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.app.IsSearching = false
		m.app.StatusMessage = "Search canceled"
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
}

func (m *Model) startTrackPlayback(trackID string) tea.Cmd {
	if strings.TrimSpace(trackID) == "" {
		return nil
	}
	if m.runtime == nil {
		m.app.StatusMessage = "Playback runtime is not configured"
		return nil
	}
	if m.session != nil {
		m.session.Stop()
		m.session = nil
	}
	m.progressActive = false
	m.progressBaseMS = 0
	m.progressSince = time.Time{}
	m.pauseRequested = false
	m.nextPlaybackID++
	playID := m.nextPlaybackID
	m.app.IsPlaying = true
	m.app.StatusMessage = "Starting playback..."
	return startPlaybackCmdWithEvents(playID, trackID, m.runtime, qualityFromConfig(m.app.Config.DefaultQuality), m.playbackEvents)
}

func (m *Model) handlePlaybackFinished() tea.Cmd {
	if m.app.QueueIndex == nil {
		m.app.IsPlaying = false
		m.app.StatusMessage = "Playback finished"
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

	m.app.IsPlaying = false
	m.app.StatusMessage = "Playback finished"
	return nil
}

func (m Model) renderSidebar(height int) string {
	nav := []string{"Browse"}
	items := []string{"Home", "Flow", "Explore", "Favorites", "Settings"}
	selectedNav := derefOrZero(m.app.NavState.Selected())
	for i, item := range items {
		prefix := "  "
		if i == selectedNav && m.app.ActivePanel == app.ActivePanelNavigation {
			prefix = "> "
		}
		nav = append(nav, prefix+item)
	}
	nav = append(nav, "", "Library")
	for i, pl := range m.app.Playlists {
		prefix := "  "
		if i == derefOrZero(m.app.PlaylistState.Selected()) && m.app.ActivePanel == app.ActivePanelPlaylists {
			prefix = "> "
		}
		nav = append(nav, prefix+truncate(pl.Title, 20))
		if i >= height-8 {
			break
		}
	}
	return m.renderPanel("Library", strings.Join(nav, "\n"), m.app.ActivePanel == app.ActivePanelNavigation || m.app.ActivePanel == app.ActivePanelPlaylists, min(m.width/4, 28), height)
}

func (m Model) renderMain(height int) string {
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
		lines = append(lines,
			" Theme         SpotifyDark",
			" Quality       320kbps",
			" Discord RPC   off",
			" Crossfade     off",
			" Config        ~/.deezer-tui-config.json",
		)
	} else if len(m.app.CurrentTracks) == 0 {
		lines = append(lines, "", " No tracks loaded")
	} else {
		if m.app.ShowingSearchResult {
			lines = append(lines, renderSearchTabs(m.app.SearchCategory))
			lines = append(lines, "")
		}
		selected := derefOrZero(m.app.MainState.Selected())
		if m.app.ShowingSearchResult {
			switch m.app.SearchCategory {
			case app.SearchCategoryTracks:
				lines = append(lines, " #  Title                               Artist")
				lines = append(lines, " ----------------------------------------------------------")
				for i, track := range m.app.CurrentTracks {
					label := fmt.Sprintf(" %02d %-35s %s", i+1, truncate(track.Title, 35), truncate(track.Artist, 18))
					if selected == i {
						label = fmt.Sprintf(">%02d %-35s %s", i+1, truncate(track.Title, 35), truncate(track.Artist, 18))
					}
					lines = append(lines, label)
				}
			case app.SearchCategoryPlaylists:
				lines = append(lines, " Playlist")
				lines = append(lines, " ---------------------------------------")
				for i, pl := range m.app.SearchPlaylists {
					label := fmt.Sprintf(" %02d %s", i+1, truncate(pl.Title, 40))
					if selected == i {
						label = fmt.Sprintf(">%02d %s", i+1, truncate(pl.Title, 40))
					}
					lines = append(lines, label)
				}
			case app.SearchCategoryArtists:
				lines = append(lines, " Artist")
				lines = append(lines, " ---------------------------------------")
				for i, artist := range m.app.SearchArtists {
					label := fmt.Sprintf(" %02d %s", i+1, truncate(artist.Name, 40))
					if selected == i {
						label = fmt.Sprintf(">%02d %s", i+1, truncate(artist.Name, 40))
					}
					lines = append(lines, label)
				}
			}
		} else {
			playAll := "  Play Collection"
			if selected == 0 {
				playAll = "> Play Collection"
			}
			lines = append(lines, playAll)
			lines = append(lines, "")
			lines = append(lines, " #  Title                               Artist")
			lines = append(lines, " ----------------------------------------------------------")
			for i, track := range m.app.CurrentTracks {
				label := fmt.Sprintf(" %02d %-35s %s", i+1, truncate(track.Title, 35), truncate(track.Artist, 18))
				if selected == i+1 {
					label = fmt.Sprintf(">%02d %-35s %s", i+1, truncate(track.Title, 35), truncate(track.Artist, 18))
				}
				lines = append(lines, label)
			}
		}
	}

	return m.renderPanel(title, strings.Join(lines, "\n"), m.app.ActivePanel == app.ActivePanelMain || m.app.ActivePanel == app.ActivePanelSearch, max(52, m.width/2), height)
}

func (m Model) renderQueue(height int) string {
	lines := []string{
		fmt.Sprintf(" State    %s", ternary(m.app.IsPlaying, "Playing", "Stopped")),
		fmt.Sprintf(" Volume   %d%%", m.app.Volume),
		fmt.Sprintf(" Repeat   %s", repeatModeLabel(m.app.RepeatMode)),
		fmt.Sprintf(" Flow     %t", m.app.IsFlowQueue),
	}

	if m.app.QueueIndex != nil && *m.app.QueueIndex < len(m.app.QueueTracks) {
		track := m.app.QueueTracks[*m.app.QueueIndex]
		lines = append(lines, "", " Now Playing", " "+truncate(track.Title, 24), " "+truncate(track.Artist, 24))
	} else {
		lines = append(lines, "", " Nothing queued")
	}

	if len(m.app.Queue) > 0 {
		lines = append(lines, "", " Queue")
		lines = append(lines, " ------------------------")
		for i, item := range m.app.Queue {
			line := fmt.Sprintf(" %02d %s", i+1, truncate(item, 22))
			if m.app.ActivePanel == app.ActivePanelQueue && i == derefOrZero(m.app.QueueState.Selected()) {
				line = fmt.Sprintf(">%02d %s", i+1, truncate(item, 22))
			} else if m.app.QueueIndex != nil && i == *m.app.QueueIndex {
				line = fmt.Sprintf("*%02d %s", i+1, truncate(item, 22))
			}
			lines = append(lines, line)
			if i >= height-10 {
				break
			}
		}
	}

	return m.renderPanel("Queue", strings.Join(lines, "\n"), m.app.ActivePanel == app.ActivePanelQueue || m.app.ActivePanel == app.ActivePanelPlayer || m.app.ActivePanel == app.ActivePanelPlayerInfo || m.app.ActivePanel == app.ActivePanelPlayerProgress, min(34, m.width/4), height)
}

func (m Model) renderSearchBar() string {
	query := m.app.SearchQuery
	if query == "" && m.app.IsSearching {
		query = "_"
	}
	label := " Search "
	if m.app.IsSearching {
		label = " Search Mode "
	}
	help := "tab switch | hjkl move | enter select | space play/pause | / search"
	return renderLineBox(fmt.Sprintf("%s %s", label, query), help, m.width)
}

func (m Model) renderPlaybar() string {
	left := " space play/pause | n/p next prev | +/- volume "
	right := fmt.Sprintf("vol %d%% | %s", m.app.Volume, ternary(m.app.IsPlaying, "playing", "paused"))
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
		" State:       " + m.displayState(),
		" Track:       " + title,
		" Artist:      " + artist,
		" Progress:    " + progress,
		" Elapsed:     " + elapsed + " / " + total,
		" Quality:     " + quality,
		" Source:      " + source,
		" Queue:       " + queueInfo,
	}
	art := m.artworkANSI
	if art == "" {
		art = strings.Join(make([]string, 7), "\n")
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
	top := tl + titleText + strings.Repeat(h, max(0, innerWidth-textWidth(titleText))) + tr
	lines := strings.Split(body, "\n")
	for len(lines) < bodyHeight {
		lines = append(lines, "")
	}
	framed := []string{top}
	for i := 0; i < bodyHeight; i++ {
		framed = append(framed, v+fitToWidth(lines[i], innerWidth)+v)
	}
	framed = append(framed, bl+strings.Repeat(h, innerWidth)+br)
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
	art := m.renderFixedBlock(content, 14, 7)
	lines := strings.Split(art, "\n")
	out := make([]string, 0, height)
	topPad := 1
	bottomPad := 1
	sidePad := 1
	for i := 0; i < topPad; i++ {
		out = append(out, strings.Repeat(" ", width))
	}
	for _, line := range lines {
		out = append(out, strings.Repeat(" ", sidePad)+fitToWidth(line, 14)+strings.Repeat(" ", sidePad))
	}
	for i := 0; i < bottomPad; i++ {
		out = append(out, strings.Repeat(" ", width))
	}
	return strings.Join(out, "\n")
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

func startPlaybackCmdWithEvents(playID int, trackID string, runtime PlayerRuntime, quality deezer.AudioQuality, events chan tea.Msg) tea.Cmd {
	if runtime == nil {
		return nil
	}
	return func() tea.Msg {
		handler := player.EventHandler{
			OnTrackChanged: func(meta deezer.TrackMetadata, q deezer.AudioQuality, initialMS uint64) {
				events <- playbackTrackChangedMsg{playID: playID, meta: meta, quality: q, initialMS: initialMS}
			},
			OnPlaybackProgress: func(currentMS, totalMS uint64) {
				events <- playbackProgressMsg{playID: playID, currentMS: currentMS, totalMS: totalMS}
			},
			OnError: func(err error) {
				events <- playbackErrorMsg{playID: playID, err: err}
			},
		}
		session, err := runtime.Start(trackID, quality, handler)
		if err != nil {
			return loadFailedMsg{message: fmt.Sprintf("Playback start error: %v", err)}
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
	bar := strings.Repeat("=", filled) + strings.Repeat("-", width-filled)
	return fmt.Sprintf("[%s] %s / %s", bar, formatClock(currentMS), formatClock(totalMS))
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
			label = "[" + strings.TrimSpace(label) + "]"
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
	return "┌" + strings.Repeat("─", inner) + "┐\n" +
		"│" + fitToWidth(left+strings.Repeat(" ", max(0, space))+right, inner) + "│\n" +
		"└" + strings.Repeat("─", inner) + "┘"
}

func renderTripleLine(left, center, right string, width int) string {
	inner := max(10, width-2)
	left = truncate(left, inner/3)
	right = truncate(right, inner/3)
	remaining := inner - textWidth(left) - textWidth(right)
	center = truncate(center, remaining)
	leftPad := max(0, (remaining-textWidth(center))/2)
	rightPad := max(0, remaining-textWidth(center)-leftPad)
	return "┌" + strings.Repeat("─", inner) + "┐\n" +
		"│" + fitToWidth(left+strings.Repeat(" ", leftPad)+center+strings.Repeat(" ", rightPad)+right, inner) + "│\n" +
		"└" + strings.Repeat("─", inner) + "┘"
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

func (m Model) displayState() string {
	status := strings.TrimSpace(m.app.StatusMessage)
	switch {
	case strings.EqualFold(status, "Buffering..."):
		return "Buffering"
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
