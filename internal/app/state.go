package app

import (
	"fmt"

	"deezer-tui/internal/colorscheme"
	"deezer-tui/internal/config"
)

type CommandKind int

const (
	CommandPlayTrack CommandKind = iota
	CommandAutoPlayTrack
	CommandPlayTrackAt
	CommandPause
	CommandResume
	CommandNext
	CommandPrevious
	CommandSetVolume
	CommandSetQuality
	CommandSetCrossfade
	CommandToggleCrossfade
	CommandLoadPlaylist
	CommandLoadHome
	CommandLoadFlow
	CommandLoadFlowPage
	CommandLoadExplore
	CommandLoadFavorites
	CommandSearch
	CommandShutdown
)

const SettingsItemCount = 6

type SearchCategory int

const (
	SearchCategoryTracks SearchCategory = iota
	SearchCategoryPlaylists
	SearchCategoryArtists
)

type RepeatMode int

const (
	RepeatModeOff RepeatMode = iota
	RepeatModeAll
	RepeatModeOne
)

type Route int

const (
	RouteLibrary Route = iota
	RouteSearch
	RouteQueue
	RouteLyrics
	RouteSettings
)

type ActivePanel int

const (
	ActivePanelNavigation ActivePanel = iota
	ActivePanelPlaylists
	ActivePanelQueue
	ActivePanelSearch
	ActivePanelMain
	ActivePanelPlayer
	ActivePanelPlayerProgress
	ActivePanelPlayerInfo
)

type ListState struct {
	selected *int
}

func (s *ListState) Select(v *int) {
	if v == nil {
		s.selected = nil
		return
	}

	value := *v
	s.selected = &value
}

func (s *ListState) Selected() *int {
	return s.selected
}

type Track struct {
	ID        string
	Title     string
	Artist    string
	Album     string
	Year      string
	AddedAtMS *int64
}

type Playlist struct {
	ID    string
	Title string
}

type Artist struct {
	ID   string
	Name string
}

type NowPlaying struct {
	ID          string
	Title       string
	Artist      string
	Quality     config.AudioQuality
	CurrentMS   uint64
	TotalMS     uint64
	AlbumArtURL *string
}

type FlowAppendResult struct {
	AppendedCount   int
	AutoplayTrackID *string
}

type App struct {
	Config              config.Config
	CurrentRoute        Route
	NowPlaying          *NowPlaying
	IsPlaying           bool
	Volume              uint16
	ActivePanel         ActivePanel
	NavState            ListState
	PlaylistState       ListState
	QueueState          ListState
	Playlists           []Playlist
	Queue               []string
	QueueTracks         []Track
	QueueIndex          *int
	CurrentTracks       []Track
	SearchPlaylists     []Playlist
	SearchArtists       []Artist
	ShowingSearchResult bool
	SearchCategory      SearchCategory
	MainState           ListState
	SettingsState       ListState
	PlayerButtonIndex   int
	PlayerInfoIndex     int
	RepeatMode          RepeatMode
	ViewingSettings     bool
	CurrentPlaylistID   *string
	StatusMessage       string
	IsSearching         bool
	SearchLoading       bool
	SearchQuery         string
	AutoTransitionArmed bool
	FlowNextIndex       int
	FlowLoadingMore     bool
	IsFlowQueue         bool
}

func New(cfg config.Config) *App {
	cfg.Theme = colorscheme.Normalize(cfg.Theme)
	app := &App{
		Config:              cfg,
		CurrentRoute:        RouteLibrary,
		IsPlaying:           false,
		Volume:              100,
		ActivePanel:         ActivePanelNavigation,
		Playlists:           []Playlist{},
		Queue:               []string{},
		QueueTracks:         []Track{},
		CurrentTracks:       []Track{},
		SearchPlaylists:     []Playlist{},
		SearchArtists:       []Artist{},
		ShowingSearchResult: false,
		SearchCategory:      SearchCategoryTracks,
		PlayerButtonIndex:   2,
		PlayerInfoIndex:     0,
		RepeatMode:          RepeatModeOff,
		ViewingSettings:     false,
		StatusMessage:       "Status: Waiting...",
		IsSearching:         false,
		SearchLoading:       false,
		SearchQuery:         "",
		AutoTransitionArmed: false,
		FlowNextIndex:       0,
		FlowLoadingMore:     false,
		IsFlowQueue:         false,
	}

	app.NavState.Select(intPtr(0))
	app.PlaylistState.Select(intPtr(0))
	app.QueueState.Select(intPtr(0))
	app.MainState.Select(intPtr(0))
	app.SettingsState.Select(intPtr(0))

	return app
}

func (a *App) HandleDown() {
	switch a.ActivePanel {
	case ActivePanelNavigation:
		current := derefOrZero(a.NavState.Selected())
		a.NavState.Select(intPtr(min(current+1, 4)))
	case ActivePanelPlaylists:
		if len(a.Playlists) == 0 {
			return
		}

		max := len(a.Playlists) - 1
		current := derefOrZero(a.PlaylistState.Selected())
		a.PlaylistState.Select(intPtr(min(current+1, max)))
	case ActivePanelQueue:
		if len(a.Queue) == 0 {
			return
		}

		max := len(a.Queue) - 1
		current := derefOrZero(a.QueueState.Selected())
		a.QueueState.Select(intPtr(min(current+1, max)))
	case ActivePanelSearch:
		a.ActivePanel = ActivePanelMain
	case ActivePanelMain:
		if a.ViewingSettings {
			current := derefOrZero(a.SettingsState.Selected())
			a.SettingsState.Select(intPtr(min(current+1, SettingsItemCount-1)))
		} else if a.ShowingSearchResult {
			max := 0
			switch a.SearchCategory {
			case SearchCategoryTracks:
				max = max0(len(a.CurrentTracks) - 1)
			case SearchCategoryPlaylists:
				max = max0(len(a.SearchPlaylists) - 1)
			case SearchCategoryArtists:
				max = max0(len(a.SearchArtists) - 1)
			}
			current := derefOrZero(a.MainState.Selected())
			a.MainState.Select(intPtr(min(current+1, max)))
		} else if len(a.CurrentTracks) > 0 {
			current := derefOrZero(a.MainState.Selected())
			a.MainState.Select(intPtr(min(current+1, len(a.CurrentTracks))))
		}
	case ActivePanelPlayer:
		a.ActivePanel = ActivePanelPlayerProgress
	case ActivePanelPlayerInfo:
		a.PlayerInfoIndex = min(a.PlayerInfoIndex+1, 1)
	}
}

func (a *App) HandleUp() {
	switch a.ActivePanel {
	case ActivePanelQueue:
		if len(a.Queue) == 0 {
			return
		}

		current := derefOrZero(a.QueueState.Selected())
		a.QueueState.Select(intPtr(max0(current - 1)))
	case ActivePanelPlaylists:
		if len(a.Playlists) == 0 {
			return
		}

		current := derefOrZero(a.PlaylistState.Selected())
		a.PlaylistState.Select(intPtr(max0(current - 1)))
	case ActivePanelNavigation:
		current := derefOrZero(a.NavState.Selected())
		a.NavState.Select(intPtr(max0(current - 1)))
	case ActivePanelSearch:
		a.ActivePanel = ActivePanelNavigation
	case ActivePanelMain:
		if a.ViewingSettings {
			current := derefOrZero(a.SettingsState.Selected())
			a.SettingsState.Select(intPtr(max0(current - 1)))
		} else if a.ShowingSearchResult {
			current := derefOrZero(a.MainState.Selected())
			a.MainState.Select(intPtr(max0(current - 1)))
		} else if len(a.CurrentTracks) > 0 {
			current := derefOrZero(a.MainState.Selected())
			a.MainState.Select(intPtr(max0(current - 1)))
		}
	case ActivePanelPlayer:
		a.ActivePanel = ActivePanelMain
	case ActivePanelPlayerProgress, ActivePanelPlayerInfo:
		a.ActivePanel = ActivePanelPlayer
	}
}

func (a *App) HandleRight() {
	switch a.ActivePanel {
	case ActivePanelNavigation, ActivePanelPlaylists:
		a.ActivePanel = ActivePanelQueue
	case ActivePanelQueue:
		a.ActivePanel = ActivePanelMain
	case ActivePanelSearch:
		a.ActivePanel = ActivePanelMain
	case ActivePanelMain:
		a.ActivePanel = ActivePanelPlayer
	case ActivePanelPlayer:
		if a.PlayerButtonIndex < 4 {
			a.PlayerButtonIndex++
		} else {
			a.ActivePanel = ActivePanelPlayerInfo
		}
	case ActivePanelPlayerProgress:
		a.ActivePanel = ActivePanelPlayerInfo
	case ActivePanelPlayerInfo:
		a.PlayerInfoIndex = min(a.PlayerInfoIndex+1, 1)
	}
}

func (a *App) HandleLeft() {
	switch a.ActivePanel {
	case ActivePanelQueue:
		a.ActivePanel = ActivePanelPlaylists
		return
	}
	switch a.ActivePanel {
	case ActivePanelMain:
		if a.ShowingSearchResult {
			switch a.SearchCategory {
			case SearchCategoryTracks:
				a.SearchCategory = SearchCategoryArtists
			case SearchCategoryPlaylists:
				a.SearchCategory = SearchCategoryTracks
			case SearchCategoryArtists:
				a.SearchCategory = SearchCategoryPlaylists
			}
			a.MainState.Select(intPtr(0))
		} else {
			a.ActivePanel = ActivePanelQueue
		}
	case ActivePanelPlayer:
		a.PlayerButtonIndex = max0(a.PlayerButtonIndex - 1)
	case ActivePanelPlayerProgress:
		a.ActivePanel = ActivePanelPlayer
	case ActivePanelPlayerInfo:
		if a.PlayerInfoIndex == 0 {
			a.ActivePanel = ActivePanelPlayer
			a.PlayerButtonIndex = 4
		} else {
			a.PlayerInfoIndex--
		}
	}
}

func (a *App) SwitchSearchCategoryRight() {
	if a.ShowingSearchResult && a.ActivePanel == ActivePanelMain {
		switch a.SearchCategory {
		case SearchCategoryTracks:
			a.SearchCategory = SearchCategoryPlaylists
		case SearchCategoryPlaylists:
			a.SearchCategory = SearchCategoryArtists
		case SearchCategoryArtists:
			a.SearchCategory = SearchCategoryTracks
		}
		a.MainState.Select(intPtr(0))
	}
}

func (a *App) LoadFlowTracks(tracks []Track, autoplay bool) *string {
	a.CurrentTracks = cloneTracks(tracks)
	a.ShowingSearchResult = false
	a.SearchLoading = false
	a.SearchPlaylists = nil
	a.SearchArtists = nil
	a.MainState.Select(intPtr(0))
	a.ViewingSettings = false
	a.ActivePanel = ActivePanelMain

	if autoplay && len(a.CurrentTracks) > 0 {
		a.QueueTracks = cloneTracks(a.CurrentTracks)
		a.Queue = formatQueue(a.QueueTracks)
		a.QueueIndex = intPtr(0)
		a.QueueState.Select(intPtr(0))
		a.IsPlaying = true
		a.IsFlowQueue = true
		return stringPtr(a.QueueTracks[0].ID)
	}

	return nil
}

func (a *App) AppendFlowTracks(tracks []Track, autoplay bool) FlowAppendResult {
	seenIDs := make(map[string]struct{}, len(a.QueueTracks))
	for _, track := range a.QueueTracks {
		seenIDs[track.ID] = struct{}{}
	}

	appendedTracks := make([]Track, 0, len(tracks))
	for _, track := range tracks {
		if _, ok := seenIDs[track.ID]; ok {
			continue
		}
		seenIDs[track.ID] = struct{}{}
		appendedTracks = append(appendedTracks, track)
	}

	if len(appendedTracks) == 0 {
		return FlowAppendResult{}
	}

	startIdx := len(a.QueueTracks)
	if a.CurrentPlaylistID != nil && *a.CurrentPlaylistID == "__flow__" {
		a.CurrentTracks = append(a.CurrentTracks, cloneTracks(appendedTracks)...)
	}
	a.QueueTracks = append(a.QueueTracks, cloneTracks(appendedTracks)...)
	a.Queue = append(a.Queue, formatQueue(appendedTracks)...)

	result := FlowAppendResult{
		AppendedCount: len(appendedTracks),
	}
	if autoplay {
		a.QueueIndex = intPtr(startIdx)
		a.QueueState.Select(intPtr(startIdx))
		a.IsPlaying = true
		a.IsFlowQueue = true
		result.AutoplayTrackID = stringPtr(a.QueueTracks[startIdx].ID)
	}

	return result
}

func (a *App) ShouldLoadMoreFlow() bool {
	if !a.IsFlowQueue || a.FlowLoadingMore || a.QueueIndex == nil {
		return false
	}

	return *a.QueueIndex+1 >= len(a.QueueTracks)
}

func formatQueue(tracks []Track) []string {
	queue := make([]string, 0, len(tracks))
	for _, track := range tracks {
		queue = append(queue, fmt.Sprintf("%s - %s", track.Title, track.Artist))
	}
	return queue
}

func cloneTracks(tracks []Track) []Track {
	out := make([]Track, len(tracks))
	copy(out, tracks)
	return out
}

func intPtr(v int) *int {
	return &v
}

func stringPtr(v string) *string {
	return &v
}

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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
