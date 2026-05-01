package deezer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultGatewayURL = "https://www.deezer.com/ajax/gw-light.php"
	defaultMediaURL   = "https://media.deezer.com/v1/get_url"
	defaultFlowAPIURL = "https://api.deezer.com"
	defaultUserAgent  = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"
)

type AudioQuality string

const (
	AudioQuality128  AudioQuality = "MP3_128"
	AudioQuality320  AudioQuality = "MP3_320"
	AudioQualityFlac AudioQuality = "FLAC"
)

type Options struct {
	HTTPClient        *http.Client
	GatewayURL        string
	MediaURL          string
	FlowAPIBaseURL    string
	UserAgent         string
	DebugDumpsEnabled bool
}

type Client struct {
	http              *http.Client
	arl               string
	sessionID         string
	licenseToken      string
	apiToken          string
	userID            string
	lovedTracksID     string
	gatewayURL        string
	mediaURL          string
	flowAPIBaseURL    string
	debugDumpsEnabled bool
}

type Track struct {
	ID     string
	Title  string
	Artist string
}

type TrackMetadata struct {
	ID           string
	Title        string
	Artist       string
	TrackToken   string
	DurationSecs *uint64
	AlbumArtURL  *string
}

type Playlist struct {
	ID    string
	Title string
}

type Artist struct {
	ID   string
	Name string
}

type PlaylistMetadata struct {
	ID     string
	Title  string
	Tracks []TrackMetadata
}

type SearchResults struct {
	Tracks    []Track
	Playlists []Playlist
	Artists   []Artist
}

type MediaRequest struct {
	TrackToken string
	Quality    AudioQuality
}

func NewClient(arl string, opts Options) (*Client, error) {
	if strings.TrimSpace(arl) == "" {
		return nil, errors.New("arl is required")
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, fmt.Errorf("build cookie jar: %w", err)
		}

		httpClient = &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		}
	}

	if opts.UserAgent == "" {
		opts.UserAgent = defaultUserAgent
	}
	if opts.GatewayURL == "" {
		opts.GatewayURL = defaultGatewayURL
	}
	if opts.MediaURL == "" {
		opts.MediaURL = defaultMediaURL
	}
	if opts.FlowAPIBaseURL == "" {
		opts.FlowAPIBaseURL = defaultFlowAPIURL
	}

	c := &Client{
		http:              httpClient,
		arl:               arl,
		gatewayURL:        opts.GatewayURL,
		mediaURL:          opts.MediaURL,
		flowAPIBaseURL:    strings.TrimRight(opts.FlowAPIBaseURL, "/"),
		debugDumpsEnabled: opts.DebugDumpsEnabled,
	}

	if err := c.seedCookies(); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Client) ARL() string           { return c.arl }
func (c *Client) UserID() string        { return c.userID }
func (c *Client) LovedTracksID() string { return c.lovedTracksID }
func (c *Client) SessionID() string     { return c.sessionID }
func (c *Client) APIToken() string      { return c.apiToken }
func (c *Client) LicenseToken() string  { return c.licenseToken }

func (c *Client) FetchAPIToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.gatewayURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", fmt.Errorf("build deezer.getUserData request: %w", err)
	}

	query := req.URL.Query()
	query.Set("method", "deezer.getUserData")
	query.Set("api_version", "1.0")
	query.Set("api_token", "")
	req.URL.RawQuery = query.Encode()
	req.Header.Set("Content-Type", "application/json")
	c.applyHeaders(req, false)

	var response map[string]any
	raw, err := c.doJSON(req, &response)
	if err != nil {
		return "", fmt.Errorf("deezer.getUserData: %w", err)
	}
	c.writeDebugDump("get_user_data_raw.json", raw)

	results := getMap(response, "results")
	c.sessionID = getString(results, "SESSION_ID")
	user := getMap(results, "USER")
	options := getMap(user, "OPTIONS")
	c.licenseToken = getString(options, "license_token")
	c.userID = getString(user, "USER_ID")
	c.lovedTracksID = getString(user, "LOVEDTRACKS_ID")
	c.apiToken = getString(results, "checkForm")

	if c.apiToken == "" {
		return "", errors.New("gateway response did not contain checkForm api token")
	}
	if err := c.seedCookies(); err != nil {
		return "", err
	}

	return c.apiToken, nil
}

func (c *Client) FetchTrackMetadata(ctx context.Context, trackID string) (TrackMetadata, error) {
	response, err := c.authenticatedGatewayCall(ctx, "deezer.pageTrack", map[string]any{
		"sng_id": trackID,
	})
	if err != nil {
		return TrackMetadata{}, err
	}

	results := getMap(response, "results")
	result := getMap(results, "DATA")
	if len(result) == 0 {
		result = results
	}

	trackToken := getString(result, "TRACK_TOKEN")
	if trackToken == "" {
		return TrackMetadata{}, errors.New("track metadata missing TRACK_TOKEN")
	}

	title := firstNonEmptyString(
		getString(result, "SNG_TITLE"),
		getString(results, "SNG_TITLE"),
		"Unknown track",
	)
	artist := firstNonEmptyString(
		getString(result, "ART_NAME"),
		getString(results, "ART_NAME"),
		"Unknown artist",
	)
	duration := getUint64Ptr(result, "DURATION")

	var albumArtURL *string
	if hash := getString(result, "ALB_PICTURE"); hash != "" {
		u := fmt.Sprintf("https://e-cdns-images.dzcdn.net/images/cover/%s/500x500-000000-80-0-0.jpg", hash)
		albumArtURL = &u
	}

	id := getString(result, "SNG_ID")
	if id == "" {
		id = trackID
	}

	return TrackMetadata{
		ID:           id,
		Title:        title,
		Artist:       artist,
		TrackToken:   trackToken,
		DurationSecs: duration,
		AlbumArtURL:  albumArtURL,
	}, nil
}

func (c *Client) FetchMediaURL(ctx context.Context, reqInput MediaRequest) (string, error) {
	if c.licenseToken == "" {
		return "", errors.New("license token not loaded; call FetchAPIToken first")
	}

	payload := map[string]any{
		"license_token": c.licenseToken,
		"media": []map[string]any{
			{
				"type": "FULL",
				"formats": []map[string]any{
					{
						"cipher": "BF_CBC_STRIPE",
						"format": string(reqInput.Quality),
					},
				},
			},
		},
		"track_tokens": []string{reqInput.TrackToken},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal media request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.mediaURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build media request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.applyHeaders(req, true)

	var response map[string]any
	_, err = c.doJSON(req, &response)
	if err != nil {
		return "", fmt.Errorf("media.get_url: %w", err)
	}

	value := response["data"]
	dataArr, _ := value.([]any)
	if len(dataArr) == 0 {
		return "", errors.New("media.get_url response missing data")
	}
	first, _ := dataArr[0].(map[string]any)
	mediaArr, _ := first["media"].([]any)
	if len(mediaArr) == 0 {
		return "", errors.New("media.get_url response missing media")
	}
	media0, _ := mediaArr[0].(map[string]any)
	sourcesArr, _ := media0["sources"].([]any)
	if len(sourcesArr) == 0 {
		return "", errors.New("media.get_url response missing sources")
	}
	source0, _ := sourcesArr[0].(map[string]any)
	signedURL := getString(source0, "url")
	if signedURL == "" {
		return "", errors.New("media.get_url response missing signed source URL")
	}
	return signedURL, nil
}

func (c *Client) FetchEncryptedBytesFromSignedURL(ctx context.Context, signedURL string) ([]byte, error) {
	return c.FetchEncryptedBytesFromSignedURLWithProgress(ctx, signedURL, nil)
}

func (c *Client) FetchEncryptedBytesFromSignedURLWithProgress(ctx context.Context, signedURL string, onProgress func(downloaded, total int64)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build signed CDN request: %w", err)
	}
	c.applyHeaders(req, true)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download encrypted audio stream: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("signed CDN request returned status %d", resp.StatusCode)
	}

	var reader io.Reader = resp.Body
	if onProgress != nil {
		reader = &progressReader{
			reader:     resp.Body,
			total:      resp.ContentLength,
			onProgress: onProgress,
		}
		onProgress(0, resp.ContentLength)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read encrypted audio bytes: %w", err)
	}
	return payload, nil
}

type progressReader struct {
	reader     io.Reader
	downloaded int64
	total      int64
	onProgress func(downloaded, total int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.downloaded += int64(n)
		r.onProgress(r.downloaded, r.total)
	}
	return n, err
}

func (c *Client) OpenSignedStream(ctx context.Context, signedURL string) (io.ReadCloser, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build signed stream request: %w", err)
	}
	c.applyHeaders(req, true)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("open signed Deezer audio stream: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		return nil, 0, fmt.Errorf("signed Deezer audio stream returned status %d", resp.StatusCode)
	}

	return resp.Body, resp.ContentLength, nil
}

func (c *Client) FetchPlaylistMetadata(ctx context.Context, playlistID string) (PlaylistMetadata, error) {
	response, err := c.authenticatedGatewayCall(ctx, "deezer.pagePlaylist", map[string]any{
		"playlist_id": playlistID,
		"lang":        "en",
	})
	if err != nil {
		return PlaylistMetadata{}, err
	}

	results := getMap(response, "results")
	title := firstNonEmptyString(getString(getMap(results, "DATA"), "TITLE"), "Unknown playlist")
	songs := getMap(results, "SONGS")
	data, _ := songs["data"].([]any)

	tracks := make([]TrackMetadata, 0, len(data))
	for _, rawTrack := range data {
		trackMap, ok := rawTrack.(map[string]any)
		if !ok {
			continue
		}
		id := getString(trackMap, "SNG_ID")
		token := getString(trackMap, "TRACK_TOKEN")
		if id == "" || token == "" {
			continue
		}
		title := firstNonEmptyString(getString(trackMap, "SNG_TITLE"), "Unknown track")
		artist := firstNonEmptyString(getString(trackMap, "ART_NAME"), "Unknown artist")
		duration := getUint64Ptr(trackMap, "DURATION")
		var albumArtURL *string
		if hash := getString(trackMap, "ALB_PICTURE"); hash != "" {
			u := fmt.Sprintf("https://e-cdns-images.dzcdn.net/images/cover/%s/500x500-000000-80-0-0.jpg", hash)
			albumArtURL = &u
		}
		tracks = append(tracks, TrackMetadata{
			ID:           id,
			Title:        title,
			Artist:       artist,
			TrackToken:   token,
			DurationSecs: duration,
			AlbumArtURL:  albumArtURL,
		})
	}

	return PlaylistMetadata{ID: playlistID, Title: title, Tracks: tracks}, nil
}

func (c *Client) FetchUserPlaylists(ctx context.Context, userID string) ([]Playlist, error) {
	if c.apiToken == "" {
		return nil, errors.New("api token not loaded; call FetchAPIToken first")
	}

	effectiveUserID := userID
	if c.userID != "" {
		effectiveUserID = c.userID
	}
	profileID, _ := strconv.ParseUint(effectiveUserID, 10, 64)

	response, _, err := c.gatewayCall(ctx, "deezer.pageProfile", map[string]any{
		"profile_id": profileID,
		"user_id":    profileID,
		"USER_ID":    profileID,
		"tab":        "playlists",
		"nb":         40,
	}, true)
	if err != nil {
		return nil, err
	}

	data, _ := getMap(getMap(getMap(response, "results"), "TAB"), "playlists")["data"].([]any)
	playlists := make([]Playlist, 0, len(data))
	for _, item := range data {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := firstNonEmptyString(getString(itemMap, "PLAYLIST_ID"), getString(itemMap, "id"))
		title := firstNonEmptyString(getString(itemMap, "TITLE"), getString(itemMap, "title"))
		if id == "" || title == "" {
			continue
		}
		playlists = append(playlists, Playlist{ID: id, Title: title})
	}
	return playlists, nil
}

func (c *Client) FetchPlaylistTracks(ctx context.Context, playlistID string) ([]Track, error) {
	if c.apiToken == "" {
		return nil, errors.New("api token not loaded; call FetchAPIToken first")
	}

	var allTracks []Track
	seenIDs := map[string]struct{}{}
	start := 0
	pageSize := 200
	var totalHint *int

	for {
		response, raw, err := c.gatewayCall(ctx, "deezer.pagePlaylist", map[string]any{
			"playlist_id": playlistID,
			"lang":        "en",
			"header":      true,
			"start":       start,
			"nb":          pageSize,
		}, true)
		if err != nil {
			return nil, err
		}
		c.writeDebugDump("playlist_tracks_raw.json", raw)

		if errValue, ok := response["error"]; ok && !isJSONEmpty(errValue) {
			return nil, fmt.Errorf("playlist gateway error: %v", errValue)
		}

		pageTracks := parseTracksFromCandidates(response,
			[]string{"results", "SONGS", "data"},
			[]string{"results", "DATA", "SONGS", "data"},
			[]string{"results", "tracks", "data"},
			[]string{"results", "TRACKS", "data"},
			[]string{"results", "tracks"},
			[]string{"results", "SONGS"},
		)

		if totalHint == nil {
			if total, ok := getIntPath(response,
				[]string{"results", "SONGS", "total"},
				[]string{"results", "SONGS", "count"},
				[]string{"results", "tracks", "total"},
			); ok {
				totalHint = &total
			}
		}

		for _, track := range pageTracks {
			if _, ok := seenIDs[track.ID]; ok {
				continue
			}
			seenIDs[track.ID] = struct{}{}
			allTracks = append(allTracks, track)
		}

		if len(pageTracks) == 0 || len(pageTracks) < pageSize {
			break
		}
		if totalHint != nil && len(allTracks) >= *totalHint {
			break
		}
		start += pageSize
	}

	return allTracks, nil
}

func (c *Client) FetchFavoriteTracks(ctx context.Context) ([]Track, error) {
	if c.lovedTracksID == "" {
		return nil, errors.New("LOVEDTRACKS_ID missing; call FetchAPIToken first")
	}
	return c.FetchPlaylistTracks(ctx, c.lovedTracksID)
}

func (c *Client) FetchSearchResults(ctx context.Context, query string) (SearchResults, error) {
	response, raw, err := c.gatewayCall(ctx, "deezer.pageSearch", map[string]any{
		"query":          query,
		"QUERY":          query,
		"start":          0,
		"nb":             50,
		"suggest":        true,
		"artist_suggest": true,
		"top_tracks":     true,
	}, true)
	if err != nil {
		return SearchResults{}, err
	}
	c.writeDebugDump("search_raw.json", raw)

	if errValue, ok := response["error"]; ok && !isJSONEmpty(errValue) {
		return SearchResults{}, fmt.Errorf("search gateway error: %v", errValue)
	}

	results := SearchResults{}
	results.Tracks = parseTracksFromCandidates(response,
		[]string{"results", "TRACK", "data"},
		[]string{"results", "SONGS", "data"},
		[]string{"results", "tracks", "data"},
	)

	playlists := getArrayPathCandidates(response,
		[]string{"results", "PLAYLIST", "data"},
		[]string{"results", "PLAYLISTS", "data"},
	)
	for _, rawPlaylist := range playlists {
		item, ok := rawPlaylist.(map[string]any)
		if !ok {
			continue
		}
		id := getString(item, "PLAYLIST_ID")
		title := firstNonEmptyString(getString(item, "TITLE"), getString(item, "title"))
		if id != "" && title != "" {
			results.Playlists = append(results.Playlists, Playlist{ID: id, Title: title})
		}
	}

	artists := getArrayPathCandidates(response,
		[]string{"results", "ARTIST", "data"},
		[]string{"results", "ARTISTS", "data"},
	)
	for _, rawArtist := range artists {
		item, ok := rawArtist.(map[string]any)
		if !ok {
			continue
		}
		id := getString(item, "ART_ID")
		name := firstNonEmptyString(getString(item, "ART_NAME"), getString(item, "name"))
		if id != "" && name != "" {
			results.Artists = append(results.Artists, Artist{ID: id, Name: name})
		}
	}

	return results, nil
}

func (c *Client) FetchHomeTracks(ctx context.Context) ([]Track, error) {
	response, raw, err := c.gatewayCall(ctx, "deezer.pageHome", map[string]any{
		"lang":  "en",
		"nb":    80,
		"start": 0,
	}, true)
	if err != nil {
		return nil, err
	}
	c.writeDebugDump("home_raw.json", raw)

	tracks := ExtractTracksRecursive(getMap(response, "results"), 120)
	if len(tracks) == 0 {
		return c.fetchHomeTracksFallback(ctx)
	}
	return tracks, nil
}

func (c *Client) FetchExploreTracks(ctx context.Context) ([]Track, error) {
	var tracks []Track
	response, raw, err := c.gatewayCall(ctx, "deezer.pageExplore", map[string]any{
		"lang":  "en",
		"nb":    80,
		"start": 0,
	}, true)
	if err == nil {
		c.writeDebugDump("explore_raw.json", raw)
		tracks = ExtractTracksRecursive(getMap(response, "results"), 120)
	} else {
		c.writeDebugDump("explore_error.json", fmt.Sprintf(`{"error":%q}`, err.Error()))
	}

	if len(tracks) == 0 {
		response, raw, err = c.gatewayCall(ctx, "deezer.pageHome", map[string]any{
			"lang":  "en",
			"tab":   "explore",
			"nb":    80,
			"start": 0,
		}, true)
		if err == nil {
			c.writeDebugDump("home_explore_tab_raw.json", raw)
			tracks = ExtractTracksRecursive(getMap(response, "results"), 120)
		}
	}

	if len(tracks) == 0 {
		return c.fetchExploreTracksFallback(ctx)
	}
	return tracks, nil
}

func (c *Client) FetchFlowTracks(ctx context.Context, index int) ([]Track, error) {
	if c.userID == "" {
		return nil, errors.New("user id missing; call FetchAPIToken first")
	}

	flowURL := fmt.Sprintf("%s/user/%s/flow", c.flowAPIBaseURL, c.userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, flowURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build flow request: %w", err)
	}
	query := req.URL.Query()
	query.Set("limit", "12")
	query.Set("index", strconv.Itoa(index))
	req.URL.RawQuery = query.Encode()
	c.applyHeaders(req, true)

	var response map[string]any
	_, err = c.doJSON(req, &response)
	if err != nil {
		return nil, fmt.Errorf("flow request: %w", err)
	}

	data, _ := response["data"].([]any)
	tracks := make([]Track, 0, len(data))
	for _, rawTrack := range data {
		item, ok := rawTrack.(map[string]any)
		if !ok {
			continue
		}
		id := firstNonEmptyString(getString(item, "id"))
		title := firstNonEmptyString(getString(item, "title"), getString(item, "title_short"), "Unknown track")
		artist := "Unknown artist"
		if artistMap, ok := item["artist"].(map[string]any); ok {
			artist = firstNonEmptyString(getString(artistMap, "name"), "Unknown artist")
		}
		if id != "" {
			tracks = append(tracks, Track{ID: id, Title: title, Artist: artist})
		}
	}

	if len(tracks) == 0 {
		return nil, errors.New("flow returned no tracks")
	}
	return tracks, nil
}

func (c *Client) authenticatedGatewayCall(ctx context.Context, method string, payload map[string]any) (map[string]any, error) {
	response, _, err := c.gatewayCall(ctx, method, payload, true)
	return response, err
}

func (c *Client) gatewayCall(ctx context.Context, method string, payload map[string]any, authenticated bool) (map[string]any, string, error) {
	if authenticated && c.apiToken == "" {
		return nil, "", errors.New("api token not loaded; call FetchAPIToken first")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("marshal gateway payload for %s: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.gatewayURL, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("build gateway request for %s: %w", method, err)
	}
	query := req.URL.Query()
	query.Set("method", method)
	query.Set("api_version", "1.0")
	query.Set("input", "3")
	if authenticated {
		query.Set("api_token", c.apiToken)
	} else {
		query.Set("api_token", "null")
	}
	req.URL.RawQuery = query.Encode()
	req.Header.Set("Content-Type", "application/json")
	c.applyHeaders(req, authenticated)

	var response map[string]any
	raw, err := c.doJSON(req, &response)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", method, err)
	}
	return response, raw, nil
}

func (c *Client) fetchHomeTracksFallback(ctx context.Context) ([]Track, error) {
	out := make([]Track, 0, 120)
	seenTrackIDs := map[string]struct{}{}

	if favorites, err := c.FetchFavoriteTracks(ctx); err == nil {
		appendUniqueTracks(&out, seenTrackIDs, favorites, 120)
	}

	if c.userID != "" {
		playlists, err := c.FetchUserPlaylists(ctx, c.userID)
		if err == nil {
			for i, playlist := range playlists {
				if i >= 8 || len(out) >= 120 {
					break
				}
				tracks, err := c.FetchPlaylistTracks(ctx, playlist.ID)
				if err == nil {
					limit := tracks
					if len(limit) > 20 {
						limit = limit[:20]
					}
					appendUniqueTracks(&out, seenTrackIDs, limit, 120)
				}
			}
		}
	}

	if len(out) == 0 {
		return nil, errors.New("home fallback produced no tracks")
	}
	return out, nil
}

func (c *Client) fetchExploreTracksFallback(ctx context.Context) ([]Track, error) {
	out := make([]Track, 0, 120)
	seenTrackIDs := map[string]struct{}{}

	seedTracks, _ := c.fetchHomeTracksFallback(ctx)
	seenArtists := map[string]struct{}{}
	artistSeeds := make([]string, 0, 12)
	for _, track := range seedTracks {
		key := strings.ToLower(track.Artist)
		if _, ok := seenArtists[key]; ok {
			continue
		}
		seenArtists[key] = struct{}{}
		artistSeeds = append(artistSeeds, track.Artist)
		if len(artistSeeds) >= 12 {
			break
		}
	}

	for _, artist := range artistSeeds {
		if len(out) >= 120 {
			break
		}
		results, err := c.FetchSearchResults(ctx, artist)
		if err == nil {
			limit := results.Tracks
			if len(limit) > 20 {
				limit = limit[:20]
			}
			appendUniqueTracks(&out, seenTrackIDs, limit, 120)
		}
	}

	if len(out) == 0 {
		return c.fetchHomeTracksFallback(ctx)
	}
	return out, nil
}

func (c *Client) seedCookies() error {
	if c.http.Jar == nil {
		return nil
	}

	targets := []string{c.gatewayURL, c.mediaURL, c.flowAPIBaseURL}
	for _, rawURL := range targets {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return fmt.Errorf("parse cookie URL %q: %w", rawURL, err)
		}

		cookies := []*http.Cookie{{Name: "arl", Value: c.arl, Path: "/", Domain: parsed.Hostname()}}
		if c.sessionID != "" {
			cookies = append(cookies, &http.Cookie{Name: "sid", Value: c.sessionID, Path: "/", Domain: parsed.Hostname()})
		}
		c.http.Jar.SetCookies(parsed, cookies)
	}
	return nil
}

func (c *Client) applyHeaders(req *http.Request, authenticated bool) {
	req.Header.Set("User-Agent", defaultUserAgent)
	cookie := "arl=" + c.arl
	if authenticated && c.sessionID != "" {
		cookie += "; sid=" + c.sessionID
	}
	req.Header.Set("Cookie", cookie)
}

func (c *Client) doJSON(req *http.Request, out *map[string]any) (string, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	rawBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	raw := string(rawBytes)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return raw, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	if err := json.Unmarshal(rawBytes, out); err != nil {
		return raw, err
	}
	return raw, nil
}

func (c *Client) writeDebugDump(fileName, raw string) {
	if !c.debugDumpsEnabled {
		return
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}
	base := filepath.Join(homeDir, ".deezer-tui-debug")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(base, fileName), []byte(raw), 0o644)
}

func appendUniqueTracks(out *[]Track, seen map[string]struct{}, tracks []Track, limit int) {
	for _, track := range tracks {
		if len(*out) >= limit {
			break
		}
		if _, ok := seen[track.ID]; ok {
			continue
		}
		seen[track.ID] = struct{}{}
		*out = append(*out, track)
	}
}

func ExtractTracksRecursive(value any, limit int) []Track {
	out := make([]Track, 0, limit)
	seen := map[string]struct{}{}
	var walk func(node any)
	walk = func(node any) {
		if len(out) >= limit {
			return
		}
		switch typed := node.(type) {
		case map[string]any:
			id := firstNonEmptyString(getString(typed, "SNG_ID"), getString(typed, "id"))
			title := firstNonEmptyString(getString(typed, "SNG_TITLE"), getString(typed, "title"))
			artist := firstNonEmptyString(
				getString(typed, "ART_NAME"),
				getString(getMap(typed, "artist"), "name"),
				getString(getMapAny(firstFromArray(getArray(typed, "ARTISTS"))), "ART_NAME"),
			)
			if id != "" && title != "" && artist != "" {
				if _, ok := seen[id]; !ok {
					seen[id] = struct{}{}
					out = append(out, Track{ID: id, Title: title, Artist: artist})
					if len(out) >= limit {
						return
					}
				}
			}
			for _, child := range typed {
				walk(child)
				if len(out) >= limit {
					return
				}
			}
		case []any:
			for _, child := range typed {
				walk(child)
				if len(out) >= limit {
					return
				}
			}
		}
	}

	walk(value)
	return out
}

func parseTracksFromCandidates(root map[string]any, paths ...[]string) []Track {
	for _, path := range paths {
		items := getArrayPath(root, path)
		if len(items) == 0 {
			continue
		}
		tracks := make([]Track, 0, len(items))
		for _, rawTrack := range items {
			item, ok := rawTrack.(map[string]any)
			if !ok {
				continue
			}
			id := firstNonEmptyString(getString(item, "SNG_ID"), getString(item, "id"))
			title := firstNonEmptyString(getString(item, "SNG_TITLE"), getString(item, "title"), "Unknown track")
			artist := firstNonEmptyString(
				getString(item, "ART_NAME"),
				getString(getMap(item, "artist"), "name"),
				getString(getMapAny(firstFromArray(getArray(item, "ARTISTS"))), "ART_NAME"),
				"Unknown artist",
			)
			if id != "" {
				tracks = append(tracks, Track{ID: id, Title: title, Artist: artist})
			}
		}
		if len(tracks) > 0 {
			return tracks
		}
	}
	return nil
}

func getMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	if raw, ok := m[key]; ok {
		if cast, ok := raw.(map[string]any); ok {
			return cast
		}
	}
	return nil
}

func getMapAny(v any) map[string]any {
	if cast, ok := v.(map[string]any); ok {
		return cast
	}
	return nil
}

func getArray(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	if raw, ok := m[key]; ok {
		if cast, ok := raw.([]any); ok {
			return cast
		}
	}
	return nil
}

func getArrayPath(root map[string]any, path []string) []any {
	current := any(root)
	for _, key := range path {
		asMap, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = asMap[key]
	}
	arr, _ := current.([]any)
	return arr
}

func getArrayPathCandidates(root map[string]any, paths ...[]string) []any {
	for _, path := range paths {
		if arr := getArrayPath(root, path); len(arr) > 0 {
			return arr
		}
	}
	return nil
}

func getIntPath(root map[string]any, paths ...[]string) (int, bool) {
	for _, path := range paths {
		current := any(root)
		ok := true
		for _, key := range path {
			asMap, isMap := current.(map[string]any)
			if !isMap {
				ok = false
				break
			}
			current = asMap[key]
		}
		if !ok {
			continue
		}
		switch v := current.(type) {
		case float64:
			return int(v), true
		case int:
			return v, true
		case string:
			if parsed, err := strconv.Atoi(v); err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}

func getString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	raw, ok := m[key]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func getUint64Ptr(m map[string]any, key string) *uint64 {
	value := getString(m, key)
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstFromArray(values []any) any {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

func isJSONEmpty(value any) bool {
	if value == nil {
		return true
	}
	switch v := value.(type) {
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	case string:
		return strings.TrimSpace(v) == ""
	default:
		return false
	}
}
