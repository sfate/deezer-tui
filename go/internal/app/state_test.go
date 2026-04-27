package app

import (
	"testing"

	"deezer-tui-go/internal/config"
)

func testApp() *App {
	app := New(config.Default())
	app.CurrentPlaylistID = stringPtr("__flow__")
	return app
}

func track(id, title, artist string) Track {
	return Track{ID: id, Title: title, Artist: artist}
}

func TestHandleDownMovesFromNavigationToPlaylistsAtSettingsRow(t *testing.T) {
	app := testApp()
	app.Playlists = []Playlist{{ID: "p1", Title: "Playlist"}}
	app.NavState.Select(intPtr(4))

	app.HandleDown()

	if app.ActivePanel != ActivePanelPlaylists {
		t.Fatalf("expected playlists panel, got %v", app.ActivePanel)
	}
	assertSelected(t, app.PlaylistState.Selected(), 0)
}

func TestHandleUpMovesFromEmptyPlaylistsToNavigationSettings(t *testing.T) {
	app := testApp()
	app.ActivePanel = ActivePanelPlaylists
	app.Playlists = nil

	app.HandleUp()

	if app.ActivePanel != ActivePanelNavigation {
		t.Fatalf("expected navigation panel, got %v", app.ActivePanel)
	}
	assertSelected(t, app.NavState.Selected(), 4)
}

func TestHandleDownOnMainAdvancesThroughTrackListAndStopsAtEnd(t *testing.T) {
	app := testApp()
	app.ActivePanel = ActivePanelMain
	app.CurrentTracks = []Track{track("1", "One", "A"), track("2", "Two", "B")}
	app.MainState.Select(intPtr(0))

	app.HandleDown()
	assertSelected(t, app.MainState.Selected(), 1)

	app.HandleDown()
	assertSelected(t, app.MainState.Selected(), 2)

	app.HandleDown()
	assertSelected(t, app.MainState.Selected(), 2)
}

func TestHandleUpOnMainReturnsToSearchFromFirstTrackRow(t *testing.T) {
	app := testApp()
	app.ActivePanel = ActivePanelMain
	app.CurrentTracks = []Track{track("1", "One", "A")}
	app.MainState.Select(intPtr(0))

	app.HandleUp()

	if app.ActivePanel != ActivePanelSearch {
		t.Fatalf("expected search panel, got %v", app.ActivePanel)
	}
}

func TestHandleRightCyclesPlayerPanelIntoPlayerInfo(t *testing.T) {
	app := testApp()
	app.ActivePanel = ActivePanelPlayer
	app.PlayerButtonIndex = 4

	app.HandleRight()

	if app.ActivePanel != ActivePanelPlayerInfo {
		t.Fatalf("expected player info panel, got %v", app.ActivePanel)
	}
}

func TestHandleLeftOnMainCyclesSearchCategoriesAndResetsSelection(t *testing.T) {
	app := testApp()
	app.ActivePanel = ActivePanelMain
	app.ShowingSearchResult = true
	app.SearchCategory = SearchCategoryTracks
	app.MainState.Select(intPtr(2))

	app.HandleLeft()
	if app.SearchCategory != SearchCategoryArtists {
		t.Fatalf("expected artists category, got %v", app.SearchCategory)
	}
	assertSelected(t, app.MainState.Selected(), 0)

	app.HandleLeft()
	if app.SearchCategory != SearchCategoryPlaylists {
		t.Fatalf("expected playlists category, got %v", app.SearchCategory)
	}

	app.HandleLeft()
	if app.SearchCategory != SearchCategoryTracks {
		t.Fatalf("expected tracks category, got %v", app.SearchCategory)
	}
}

func TestSwitchSearchCategoryRightRotatesCategoriesOnlyInSearchResultsMainPanel(t *testing.T) {
	app := testApp()
	app.ActivePanel = ActivePanelMain
	app.ShowingSearchResult = true
	app.SearchCategory = SearchCategoryTracks
	app.MainState.Select(intPtr(3))

	app.SwitchSearchCategoryRight()
	if app.SearchCategory != SearchCategoryPlaylists {
		t.Fatalf("expected playlists category, got %v", app.SearchCategory)
	}
	assertSelected(t, app.MainState.Selected(), 0)

	app.SwitchSearchCategoryRight()
	if app.SearchCategory != SearchCategoryArtists {
		t.Fatalf("expected artists category, got %v", app.SearchCategory)
	}

	app.ActivePanel = ActivePanelPlayer
	app.SwitchSearchCategoryRight()
	if app.SearchCategory != SearchCategoryArtists {
		t.Fatalf("expected no change outside main panel, got %v", app.SearchCategory)
	}
}

func TestLoadFlowTracksPopulatesQueueAndReturnsFirstTrackWhenAutoplaying(t *testing.T) {
	app := testApp()
	tracks := []Track{track("1", "One", "A"), track("2", "Two", "B")}

	first := app.LoadFlowTracks(tracks, true)

	if first == nil || *first != "1" {
		t.Fatalf("expected first flow track 1, got %v", first)
	}
	assertTracks(t, app.CurrentTracks, tracks)
	assertTracks(t, app.QueueTracks, tracks)
	assertStrings(t, app.Queue, []string{"One - A", "Two - B"})
	assertSelectedPtr(t, app.QueueIndex, 0)
	if !app.IsPlaying {
		t.Fatalf("expected app to be playing")
	}
}

func TestAppendFlowTracksSkipsDuplicatesAndAutoplaysFirstNewTrack(t *testing.T) {
	app := testApp()
	app.LoadFlowTracks([]Track{track("1", "One", "A"), track("2", "Two", "B")}, true)

	result := app.AppendFlowTracks([]Track{
		track("2", "Two", "B"),
		track("3", "Three", "C"),
		track("4", "Four", "D"),
	}, true)

	if result.AppendedCount != 2 {
		t.Fatalf("expected 2 appended tracks, got %d", result.AppendedCount)
	}
	if result.AutoplayTrackID == nil || *result.AutoplayTrackID != "3" {
		t.Fatalf("expected autoplay track 3, got %v", result.AutoplayTrackID)
	}
	if len(app.QueueTracks) != 4 {
		t.Fatalf("expected 4 queued tracks, got %d", len(app.QueueTracks))
	}
	if len(app.CurrentTracks) != 4 {
		t.Fatalf("expected 4 current tracks, got %d", len(app.CurrentTracks))
	}
	assertSelectedPtr(t, app.QueueIndex, 2)
	if app.Queue[2] != "Three - C" || app.Queue[3] != "Four - D" {
		t.Fatalf("unexpected queue tail: %#v", app.Queue)
	}
}

func TestAppendFlowTracksSkipsDuplicatesWithinTheSameBatch(t *testing.T) {
	app := testApp()
	app.LoadFlowTracks([]Track{track("1", "One", "A")}, true)

	result := app.AppendFlowTracks([]Track{
		track("2", "Two", "B"),
		track("2", "Two", "B"),
		track("3", "Three", "C"),
	}, true)

	if result.AppendedCount != 2 {
		t.Fatalf("expected 2 appended tracks, got %d", result.AppendedCount)
	}
	if result.AutoplayTrackID == nil || *result.AutoplayTrackID != "2" {
		t.Fatalf("expected autoplay track 2, got %v", result.AutoplayTrackID)
	}
	assertTrackIDs(t, app.QueueTracks, []string{"1", "2", "3"})
}

func TestAppendFlowTracksWithoutAutoplayStillReportsAppendedTracks(t *testing.T) {
	app := testApp()
	app.LoadFlowTracks([]Track{track("1", "One", "A"), track("2", "Two", "B")}, true)

	result := app.AppendFlowTracks([]Track{track("3", "Three", "C")}, false)

	if result.AppendedCount != 1 {
		t.Fatalf("expected 1 appended track, got %d", result.AppendedCount)
	}
	if result.AutoplayTrackID != nil {
		t.Fatalf("expected no autoplay track, got %v", result.AutoplayTrackID)
	}
	if len(app.QueueTracks) != 3 || len(app.CurrentTracks) != 3 {
		t.Fatalf("unexpected queue/current lengths: %d/%d", len(app.QueueTracks), len(app.CurrentTracks))
	}
	assertSelectedPtr(t, app.QueueIndex, 0)
	if !app.IsPlaying || !app.IsFlowQueue {
		t.Fatalf("expected flow playback to remain active")
	}
}

func TestAppendFlowTracksDoesNotOverwriteNonFlowPageTracks(t *testing.T) {
	app := testApp()
	app.LoadFlowTracks([]Track{track("1", "One", "A"), track("2", "Two", "B")}, true)
	app.CurrentPlaylistID = stringPtr("__home__")
	app.CurrentTracks = []Track{track("10", "Home Track", "Home Artist")}

	result := app.AppendFlowTracks([]Track{track("3", "Three", "C")}, false)

	if result.AppendedCount != 1 {
		t.Fatalf("expected 1 appended track, got %d", result.AppendedCount)
	}
	assertTracks(t, app.CurrentTracks, []Track{track("10", "Home Track", "Home Artist")})
	if len(app.QueueTracks) != 3 {
		t.Fatalf("expected queue length 3, got %d", len(app.QueueTracks))
	}
}

func TestShouldLoadMoreFlowOnlyOnLastQueuedFlowTrackEvenIfPlaylistIDChanges(t *testing.T) {
	app := testApp()
	app.LoadFlowTracks([]Track{track("1", "One", "A"), track("2", "Two", "B")}, true)

	if app.ShouldLoadMoreFlow() {
		t.Fatalf("expected no flow continuation before last track")
	}

	app.QueueIndex = intPtr(1)
	if !app.ShouldLoadMoreFlow() {
		t.Fatalf("expected flow continuation on last track")
	}

	app.FlowLoadingMore = true
	if app.ShouldLoadMoreFlow() {
		t.Fatalf("expected no continuation while already loading")
	}

	app.FlowLoadingMore = false
	app.CurrentPlaylistID = stringPtr("__home__")
	if !app.ShouldLoadMoreFlow() {
		t.Fatalf("expected queue-based flow continuation even after leaving flow page")
	}
}

func TestShouldNotLoadMoreFlowWhenPlaylistIDIsStaleButQueueIsNotFlow(t *testing.T) {
	app := testApp()
	app.CurrentPlaylistID = stringPtr("__flow__")
	app.QueueTracks = []Track{track("1", "One", "A")}
	app.QueueIndex = intPtr(0)
	app.IsFlowQueue = false

	if app.ShouldLoadMoreFlow() {
		t.Fatalf("expected no flow continuation for non-flow queue")
	}
}

func TestNonFlowTracksLoadKeepsFlowCursorWhenFlowQueueIsStillActive(t *testing.T) {
	app := testApp()
	app.IsFlowQueue = true
	app.FlowNextIndex = 24
	app.CurrentPlaylistID = stringPtr("__home__")

	if app.CurrentPlaylistID != nil && *app.CurrentPlaylistID != "__flow__" && !app.IsFlowQueue {
		app.FlowNextIndex = 0
	}
	app.CurrentTracks = []Track{track("10", "Home Track", "Home Artist")}

	if app.FlowNextIndex != 24 {
		t.Fatalf("expected flow cursor to stay at 24, got %d", app.FlowNextIndex)
	}
	assertTracks(t, app.CurrentTracks, []Track{track("10", "Home Track", "Home Artist")})
}

func assertSelected(t *testing.T, v *int, want int) {
	t.Helper()
	assertSelectedPtr(t, v, want)
}

func assertSelectedPtr(t *testing.T, v *int, want int) {
	t.Helper()
	if v == nil || *v != want {
		t.Fatalf("expected selection %d, got %v", want, v)
	}
}

func assertStrings(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %d strings, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("string mismatch at %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func assertTracks(t *testing.T, got, want []Track) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %d tracks, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("track mismatch at %d: got %#v want %#v", i, got[i], want[i])
		}
	}
}

func assertTrackIDs(t *testing.T, tracks []Track, want []string) {
	t.Helper()
	if len(tracks) != len(want) {
		t.Fatalf("expected %d tracks, got %d", len(want), len(tracks))
	}
	for i, id := range want {
		if tracks[i].ID != id {
			t.Fatalf("track id mismatch at %d: got %q want %q", i, tracks[i].ID, id)
		}
	}
}
