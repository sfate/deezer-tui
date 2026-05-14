package deezer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchAPITokenBootstrapsSessionState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("method") != "deezer.getUserData" {
			t.Fatalf("unexpected method %q", r.URL.Query().Get("method"))
		}
		if !strings.Contains(r.Header.Get("Cookie"), "arl=test-arl") {
			t.Fatalf("expected arl cookie, got %q", r.Header.Get("Cookie"))
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": map[string]any{
				"SESSION_ID": "sid-123",
				"checkForm":  "api-456",
				"USER": map[string]any{
					"USER_ID":        42,
					"LOVEDTRACKS_ID": 99,
					"OPTIONS": map[string]any{
						"license_token": "license-789",
					},
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewClient("test-arl", Options{
		GatewayURL:     server.URL,
		MediaURL:       server.URL,
		FlowAPIBaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	token, err := client.FetchAPIToken(context.Background())
	if err != nil {
		t.Fatalf("fetch api token: %v", err)
	}

	if token != "api-456" {
		t.Fatalf("unexpected token %q", token)
	}
	if client.SessionID() != "sid-123" || client.UserID() != "42" || client.LovedTracksID() != "99" {
		t.Fatalf("unexpected bootstrapped client state: session=%q user=%q loved=%q", client.SessionID(), client.UserID(), client.LovedTracksID())
	}
	if client.LicenseToken() != "license-789" {
		t.Fatalf("unexpected license token %q", client.LicenseToken())
	}
}

func TestFetchAPITokenUsesCustomUserAgent(t *testing.T) {
	const userAgent = "deezer-tui-test-agent"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Fatalf("expected custom user agent %q, got %q", userAgent, got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": map[string]any{"checkForm": "api-token"}})
	}))
	defer server.Close()

	client, err := NewClient("test-arl", Options{
		GatewayURL:     server.URL,
		MediaURL:       server.URL,
		FlowAPIBaseURL: server.URL,
		UserAgent:      userAgent,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := client.FetchAPIToken(context.Background()); err != nil {
		t.Fatalf("fetch api token: %v", err)
	}
}

func TestFetchFlowTracksUsesAuthenticatedCookiesAndPagination(t *testing.T) {
	client, err := NewClient("test-arl", Options{})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	client.userID = "42"
	client.sessionID = "sid-123"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/42/flow" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if r.URL.Query().Get("index") != "12" || r.URL.Query().Get("limit") != "12" {
			t.Fatalf("unexpected query %q", r.URL.RawQuery)
		}
		cookie := r.Header.Get("Cookie")
		if !strings.Contains(cookie, "arl=test-arl") || !strings.Contains(cookie, "sid=sid-123") {
			t.Fatalf("expected arl+sid cookies, got %q", cookie)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": 1, "title": "One", "artist": map[string]any{"name": "A"}},
				{"id": 2, "title_short": "Two", "artist": map[string]any{"name": "B"}},
			},
		})
	}))
	defer server.Close()

	client.flowAPIBaseURL = server.URL
	if err := client.seedCookies(); err != nil {
		t.Fatalf("seed cookies: %v", err)
	}

	tracks, err := client.FetchFlowTracks(context.Background(), 12)
	if err != nil {
		t.Fatalf("fetch flow tracks: %v", err)
	}

	if len(tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %d", len(tracks))
	}
	if tracks[0] != (Track{ID: "1", Title: "One", Artist: "A"}) {
		t.Fatalf("unexpected first track: %#v", tracks[0])
	}
	if tracks[1] != (Track{ID: "2", Title: "Two", Artist: "B"}) {
		t.Fatalf("unexpected second track: %#v", tracks[1])
	}
}

func TestExtractTracksRecursiveFindsNestedUniqueTracks(t *testing.T) {
	value := map[string]any{
		"foo": []any{
			map[string]any{
				"SNG_ID":    "1",
				"SNG_TITLE": "One",
				"ART_NAME":  "A",
			},
			map[string]any{
				"nested": map[string]any{
					"id":    2,
					"title": "Two",
					"artist": map[string]any{
						"name": "B",
					},
				},
			},
			map[string]any{
				"SNG_ID":    "1",
				"SNG_TITLE": "One",
				"ART_NAME":  "A",
			},
		},
	}

	tracks := ExtractTracksRecursive(value, 10)

	if len(tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %d", len(tracks))
	}
	if tracks[0] != (Track{ID: "1", Title: "One", Artist: "A"}) {
		t.Fatalf("unexpected first track: %#v", tracks[0])
	}
	if tracks[1] != (Track{ID: "2", Title: "Two", Artist: "B"}) {
		t.Fatalf("unexpected second track: %#v", tracks[1])
	}
}

func TestFetchSearchResultsParsesTracksPlaylistsAndArtists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": map[string]any{
				"TRACK": map[string]any{
					"data": []map[string]any{
						{"SNG_ID": "1", "SNG_TITLE": "One", "ART_NAME": "A", "ALB_TITLE": "Album", "PHYSICAL_RELEASE_DATE": "2024-03-01"},
					},
				},
				"PLAYLIST": map[string]any{
					"data": []map[string]any{
						{"PLAYLIST_ID": "10", "TITLE": "P"},
					},
				},
				"ARTIST": map[string]any{
					"data": []map[string]any{
						{"ART_ID": "20", "ART_NAME": "Artist"},
					},
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewClient("test-arl", Options{
		GatewayURL:     server.URL,
		MediaURL:       server.URL,
		FlowAPIBaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	client.apiToken = "api-456"
	client.sessionID = "sid-123"

	results, err := client.FetchSearchResults(context.Background(), "query")
	if err != nil {
		t.Fatalf("fetch search results: %v", err)
	}

	if len(results.Tracks) != 1 || len(results.Playlists) != 1 || len(results.Artists) != 1 {
		t.Fatalf("unexpected search result sizes: tracks=%d playlists=%d artists=%d", len(results.Tracks), len(results.Playlists), len(results.Artists))
	}
	if results.Tracks[0].Album != "Album" || results.Tracks[0].Year != "2024" {
		t.Fatalf("unexpected search track album/year: %#v", results.Tracks[0])
	}
}

func TestParseTrackYearUsesNestedAlbumReleaseDate(t *testing.T) {
	got := parseTrackYear(map[string]any{
		"ALBUM": map[string]any{
			"RELEASE_DATE": "2021-09-17",
		},
	})
	if got != "2021" {
		t.Fatalf("expected nested album release year, got %q", got)
	}
}

func TestFetchSearchResultsEnrichesMissingYearFromTrackAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/track/1" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"release_date": "2020-05-15",
				"album": map[string]any{
					"title": "API Album",
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": map[string]any{
				"TRACK": map[string]any{
					"data": []map[string]any{
						{"SNG_ID": "1", "SNG_TITLE": "One", "ART_NAME": "A"},
					},
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewClient("test-arl", Options{
		GatewayURL:     server.URL,
		MediaURL:       server.URL,
		FlowAPIBaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	client.apiToken = "api-456"
	client.sessionID = "sid-123"

	results, err := client.FetchSearchResults(context.Background(), "query")
	if err != nil {
		t.Fatalf("fetch search results: %v", err)
	}
	if len(results.Tracks) != 1 {
		t.Fatalf("expected one track, got %d", len(results.Tracks))
	}
	if results.Tracks[0].Year != "2020" || results.Tracks[0].Album != "API Album" {
		t.Fatalf("expected enriched album/year, got %#v", results.Tracks[0])
	}
}

func TestFetchEncryptedBytesReportsDownloadProgress(t *testing.T) {
	payload := []byte(strings.Repeat("x", 1024))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		_, _ = w.Write(payload[:256])
		_, _ = w.Write(payload[256:])
	}))
	defer server.Close()

	client, err := NewClient("test-arl", Options{})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var seen []int64
	got, err := client.FetchEncryptedBytesFromSignedURLWithProgress(context.Background(), server.URL, func(downloaded, total int64) {
		if total != int64(len(payload)) {
			t.Fatalf("expected total %d, got %d", len(payload), total)
		}
		seen = append(seen, downloaded)
	})
	if err != nil {
		t.Fatalf("fetch encrypted bytes: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatal("unexpected downloaded payload")
	}
	if len(seen) < 2 || seen[0] != 0 || seen[len(seen)-1] != int64(len(payload)) {
		t.Fatalf("unexpected progress samples: %#v", seen)
	}
}
