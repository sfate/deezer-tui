package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"deezer-tui-go/internal/app"
	"deezer-tui-go/internal/config"
)

type Model struct {
	app    *app.App
	width  int
	height int
}

func New() Model {
	state := app.New(config.Default())
	seedApp(state)

	return Model{
		app: state,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q":
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
			m.handleEnter()
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
		m.app.IsPlaying = false
		m.app.StatusMessage = "Paused (audio backend not wired yet)"
		return
	}
	if len(m.app.QueueTracks) == 0 && len(m.app.CurrentTracks) > 0 {
		m.app.QueueTracks = append([]app.Track(nil), m.app.CurrentTracks...)
		m.app.Queue = formatQueue(m.app.QueueTracks)
		m.app.QueueIndex = intPtr(0)
		m.app.QueueState.Select(intPtr(0))
	}
	if len(m.app.QueueTracks) > 0 {
		m.app.IsPlaying = true
		m.app.StatusMessage = "Playing (audio backend not wired yet)"
	}
}

func (m *Model) handleEnter() {
	switch m.app.ActivePanel {
	case app.ActivePanelNavigation:
		index := derefOrZero(m.app.NavState.Selected())
		switch index {
		case 0:
			m.loadStaticCollection("__home__", "Home", sampleHomeTracks())
		case 1:
			m.app.CurrentPlaylistID = stringPtr("__flow__")
			m.app.FlowNextIndex = len(sampleFlowTracks())
			m.app.LoadFlowTracks(sampleFlowTracks(), true)
			m.app.StatusMessage = fmt.Sprintf("Loaded Flow (%d tracks)", len(m.app.CurrentTracks))
		case 2:
			m.loadStaticCollection("__explore__", "Explore", sampleExploreTracks())
		case 3:
			m.loadStaticCollection("__favorites__", "Favorites", sampleFavoriteTracks())
		case 4:
			m.app.ViewingSettings = true
			m.app.ActivePanel = app.ActivePanelMain
			m.app.StatusMessage = "Settings placeholder"
		}
	case app.ActivePanelPlaylists:
		if len(m.app.Playlists) == 0 {
			return
		}
		idx := derefOrZero(m.app.PlaylistState.Selected())
		if idx >= len(m.app.Playlists) {
			return
		}
		pl := m.app.Playlists[idx]
		m.loadStaticCollection(pl.ID, pl.Title, samplePlaylistTracks(pl.ID))
	case app.ActivePanelMain:
		if m.app.ViewingSettings {
			m.app.StatusMessage = "Settings action not wired yet"
			return
		}
		if len(m.app.CurrentTracks) == 0 {
			return
		}
		selected := derefOrZero(m.app.MainState.Selected())
		if selected == 0 {
			m.app.QueueTracks = append([]app.Track(nil), m.app.CurrentTracks...)
			m.app.Queue = formatQueue(m.app.QueueTracks)
			m.app.QueueIndex = intPtr(0)
			m.app.QueueState.Select(intPtr(0))
			m.app.IsPlaying = true
			m.app.StatusMessage = fmt.Sprintf("Queued %d tracks", len(m.app.QueueTracks))
			return
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
		}
	}
}

func (m *Model) loadStaticCollection(id, title string, tracks []app.Track) {
	m.app.CurrentPlaylistID = stringPtr(id)
	m.app.CurrentTracks = append([]app.Track(nil), tracks...)
	m.app.MainState.Select(intPtr(0))
	m.app.SearchPlaylists = nil
	m.app.SearchArtists = nil
	m.app.ShowingSearchResult = false
	m.app.ViewingSettings = false
	m.app.ActivePanel = app.ActivePanelMain
	if id != "__flow__" && !m.app.IsFlowQueue {
		m.app.FlowNextIndex = 0
	}
	m.app.StatusMessage = fmt.Sprintf("Loaded %s (%d tracks)", title, len(tracks))
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
			"  ARL: configured in Rust app",
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

func seedApp(a *app.App) {
	a.Playlists = []app.Playlist{
		{ID: "pl-1", Title: "Morning Run"},
		{ID: "pl-2", Title: "Focus Mix"},
		{ID: "pl-3", Title: "Late Night"},
	}
	a.CurrentPlaylistID = stringPtr("__home__")
	a.CurrentTracks = sampleHomeTracks()
	a.MainState.Select(intPtr(0))
	a.StatusMessage = "Bubble Tea shell ready"
}

func sampleHomeTracks() []app.Track {
	return []app.Track{
		{ID: "101", Title: "Northern Lights", Artist: "Kira Vale"},
		{ID: "102", Title: "Afterglow", Artist: "June Arcade"},
		{ID: "103", Title: "Signal Fade", Artist: "Static Youth"},
	}
}

func sampleFlowTracks() []app.Track {
	return []app.Track{
		{ID: "201", Title: "Current Drift", Artist: "Velvet Echo"},
		{ID: "202", Title: "Night Transit", Artist: "Aster Lane"},
		{ID: "203", Title: "Pulse Memory", Artist: "Blue Halcyon"},
	}
}

func sampleExploreTracks() []app.Track {
	return []app.Track{
		{ID: "301", Title: "Golden Frame", Artist: "Suna"},
		{ID: "302", Title: "Warm Circuit", Artist: "Yard Static"},
		{ID: "303", Title: "Daybreak Motel", Artist: "Mina Rowe"},
	}
}

func sampleFavoriteTracks() []app.Track {
	return []app.Track{
		{ID: "401", Title: "Tidal Glass", Artist: "Arlo Finch"},
		{ID: "402", Title: "Velour Skies", Artist: "Nina Crest"},
		{ID: "403", Title: "Archive 94", Artist: "The Meridian"},
	}
}

func samplePlaylistTracks(id string) []app.Track {
	switch id {
	case "pl-1":
		return []app.Track{
			{ID: "501", Title: "Stride", Artist: "Mosaic Club"},
			{ID: "502", Title: "Pavement Heat", Artist: "Rin Moto"},
			{ID: "503", Title: "Breathing Room", Artist: "Lio Park"},
		}
	case "pl-2":
		return []app.Track{
			{ID: "601", Title: "Worklight", Artist: "Grey Atlas"},
			{ID: "602", Title: "Signal Desk", Artist: "Paper Harbor"},
			{ID: "603", Title: "Quiet Engine", Artist: "Taro Bloom"},
		}
	default:
		return []app.Track{
			{ID: "701", Title: "Red Window", Artist: "Cinder Vale"},
			{ID: "702", Title: "Taxi Static", Artist: "Neon District"},
			{ID: "703", Title: "Sleep Dealer", Artist: "Marlowe"},
		}
	}
}

func formatQueue(tracks []app.Track) []string {
	queue := make([]string, 0, len(tracks))
	for _, track := range tracks {
		queue = append(queue, track.Title+" - "+track.Artist)
	}
	return queue
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
