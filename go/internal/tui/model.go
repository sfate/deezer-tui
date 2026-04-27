package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

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

type playbackStartedMsg struct {
	playID  int
	session PlaybackSession
}

type playbackFinishedMsg struct {
	playID int
	err    error
}

type Model struct {
	app            *app.App
	loader         Loader
	runtime        PlayerRuntime
	session        PlaybackSession
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
		app:     state,
		loader:  loader,
		runtime: newPlayerRuntime(loader),
	}
}

func NewWithLoader(cfg config.Config, loader Loader) Model {
	state := app.New(cfg)
	state.StatusMessage = "Loading Deezer library..."
	if loader == nil {
		state.StatusMessage = "No Deezer loader configured"
	}
	return Model{
		app:     state,
		loader:  loader,
		runtime: newPlayerRuntime(loader),
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
		m.app.StatusMessage = "Playing"
		return m, waitPlaybackCmd(msg.playID, msg.session)
	case playbackFinishedMsg:
		if msg.playID != m.currentPlayID {
			return m, nil
		}
		m.session = nil
		if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
			m.app.IsPlaying = false
			m.app.StatusMessage = fmt.Sprintf("Playback error: %v", msg.err)
			return m, nil
		}
		if errors.Is(msg.err, context.Canceled) {
			return m, nil
		}
		return m, m.handlePlaybackFinished()
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyPressMsg:
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
		case " ":
			m.togglePlayPause()
		case "p":
			m.togglePlayPause()
		case "/":
			m.app.StatusMessage = "Search input not wired yet in Go shell"
			m.app.ActivePanel = app.ActivePanelSearch
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

	header := "Deezer TUI Go Rewrite"
	body := joinColumns(
		m.renderNavigation(),
		m.renderPlaylists(),
		m.renderMain(),
		m.renderPlayer(),
	)
	footer := "Arrows/HJKL navigate | TAB switch focus | Enter select | P/Space play/pause | Q quit"

	content := strings.Join([]string{
		header,
		"",
		body,
		"",
		"Status: " + m.app.StatusMessage,
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
		}
		m.app.IsPlaying = false
		m.app.StatusMessage = "Paused"
		return
	}
	if m.session != nil {
		m.session.Resume()
		m.app.IsPlaying = true
		m.app.StatusMessage = "Playing"
		return
	}
	if len(m.app.QueueTracks) == 0 && len(m.app.CurrentTracks) > 0 {
		m.app.QueueTracks = append([]app.Track(nil), m.app.CurrentTracks...)
		m.app.Queue = formatQueue(m.app.QueueTracks)
		m.app.QueueIndex = intPtr(0)
		m.app.QueueState.Select(intPtr(0))
	}
	if len(m.app.QueueTracks) > 0 && m.app.QueueIndex != nil && *m.app.QueueIndex < len(m.app.QueueTracks) {
		m.app.IsPlaying = true
		m.app.StatusMessage = "Starting playback..."
	}
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
	case app.ActivePanelMain:
		if m.app.ViewingSettings {
			m.app.StatusMessage = "Settings actions not wired yet"
			return nil
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
	m.nextPlaybackID++
	playID := m.nextPlaybackID
	m.app.IsPlaying = true
	m.app.StatusMessage = "Starting playback..."
	return startPlaybackCmd(playID, trackID, m.runtime, qualityFromConfig(m.app.Config.DefaultQuality))
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

func (m Model) renderNavigation() string {
	items := []string{"Home", "Flow", "Explore", "Favorites", "Settings"}
	lines := make([]string, 0, len(items))
	selected := derefOrZero(m.app.NavState.Selected())
	for i, item := range items {
		line := "  " + item
		if i == selected {
			line = "> " + item
		}
		lines = append(lines, line)
	}
	return m.renderPanel("Navigation", strings.Join(lines, "\n"), m.app.ActivePanel == app.ActivePanelNavigation)
}

func (m Model) renderPlaylists() string {
	lines := make([]string, 0, len(m.app.Playlists))
	selected := derefOrZero(m.app.PlaylistState.Selected())
	if len(m.app.Playlists) == 0 {
		lines = append(lines, "No playlists")
	} else {
		for i, item := range m.app.Playlists {
			line := "  " + item.Title
			if i == selected {
				line = "> " + item.Title
			}
			lines = append(lines, line)
		}
	}
	return m.renderPanel("Playlists", strings.Join(lines, "\n"), m.app.ActivePanel == app.ActivePanelPlaylists)
}

func (m Model) renderMain() string {
	var title string
	switch {
	case m.app.ViewingSettings:
		title = "Settings"
	case m.app.CurrentPlaylistID != nil:
		title = *m.app.CurrentPlaylistID
	default:
		title = "Main"
	}

	lines := []string{}
	if m.app.ViewingSettings {
		lines = append(lines,
			"  Theme: SpotifyDark",
			"  Quality: 320kbps",
			"  Discord RPC: off",
			"  Crossfade: off",
			"  ARL: loaded from ~/.deezer-tui-config.json",
		)
	} else if len(m.app.CurrentTracks) == 0 {
		lines = append(lines, "No tracks loaded")
	} else {
		selected := derefOrZero(m.app.MainState.Selected())
		playAll := "  Play Collection"
		if selected == 0 {
			playAll = "> Play Collection"
		}
		lines = append(lines, playAll)
		for i, track := range m.app.CurrentTracks {
			label := fmt.Sprintf("  %s - %s", track.Title, track.Artist)
			if selected == i+1 {
				label = "> " + track.Title + " - " + track.Artist
			}
			lines = append(lines, label)
		}
	}

	return m.renderPanel(title, strings.Join(lines, "\n"), m.app.ActivePanel == app.ActivePanelMain || m.app.ActivePanel == app.ActivePanelSearch)
}

func (m Model) renderPlayer() string {
	lines := []string{
		fmt.Sprintf("State: %s", ternary(m.app.IsPlaying, "Playing", "Stopped")),
		fmt.Sprintf("Volume: %d%%", m.app.Volume),
		fmt.Sprintf("Repeat: %s", repeatModeLabel(m.app.RepeatMode)),
		fmt.Sprintf("Flow Queue: %t", m.app.IsFlowQueue),
	}

	if m.app.QueueIndex != nil && *m.app.QueueIndex < len(m.app.QueueTracks) {
		track := m.app.QueueTracks[*m.app.QueueIndex]
		lines = append(lines, "", track.Title, track.Artist)
	} else {
		lines = append(lines, "", "Nothing queued")
	}

	if len(m.app.Queue) > 0 {
		lines = append(lines, "", "Queue:")
		for i, item := range m.app.Queue {
			line := "  " + item
			if m.app.QueueIndex != nil && i == *m.app.QueueIndex {
				line = "> " + item
			}
			lines = append(lines, line)
			if i >= 5 {
				break
			}
		}
	}

	return m.renderPanel("Player", strings.Join(lines, "\n"), m.app.ActivePanel == app.ActivePanelPlayer || m.app.ActivePanel == app.ActivePanelPlayerInfo || m.app.ActivePanel == app.ActivePanelPlayerProgress)
}

func (m Model) renderPanel(title, body string, active bool) string {
	marker := " "
	if active {
		marker = "*"
	}
	header := fmt.Sprintf("%s %s", marker, title)
	return padBlock(header+"\n"+body, max0(m.width/4-2), 18)
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

func startPlaybackCmd(playID int, trackID string, runtime PlayerRuntime, quality deezer.AudioQuality) tea.Cmd {
	if runtime == nil {
		return nil
	}
	return func() tea.Msg {
		session, err := runtime.Start(trackID, quality, player.EventHandler{})
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
	if v < 0 {
		return 0
	}
	return v
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
			if len(line) > widths[i] {
				widths[i] = len(line)
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

func padBlock(content string, width, height int) string {
	lines := strings.Split(content, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = padRight(line, width)
	}
	return strings.Join(lines[:height], "\n")
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
