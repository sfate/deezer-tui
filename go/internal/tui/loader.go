package tui

import (
	"context"
	"fmt"

	"deezer-tui-go/internal/app"
	"deezer-tui-go/internal/config"
	"deezer-tui-go/internal/deezer"
	"deezer-tui-go/internal/player"
)

type BootstrapData struct {
	Playlists []app.Playlist
}

type Loader interface {
	Bootstrap(ctx context.Context) (BootstrapData, error)
	LoadHome(ctx context.Context) ([]app.Track, error)
	LoadFlow(ctx context.Context, index int) ([]app.Track, error)
	LoadExplore(ctx context.Context) ([]app.Track, error)
	LoadFavorites(ctx context.Context) ([]app.Track, error)
	LoadPlaylist(ctx context.Context, playlistID string) ([]app.Track, error)
}

type DeezerLoader struct {
	client *deezer.Client
}

func NewDeezerLoader(cfg config.Config) (*DeezerLoader, error) {
	client, err := deezer.NewClient(cfg.ARL, deezer.Options{})
	if err != nil {
		return nil, err
	}
	return &DeezerLoader{client: client}, nil
}

func (l *DeezerLoader) Bootstrap(ctx context.Context) (BootstrapData, error) {
	if _, err := l.client.FetchAPIToken(ctx); err != nil {
		return BootstrapData{}, fmt.Errorf("fetch Deezer session: %w", err)
	}
	playlists, err := l.client.FetchUserPlaylists(ctx, l.client.UserID())
	if err != nil {
		return BootstrapData{}, fmt.Errorf("fetch playlists: %w", err)
	}
	return BootstrapData{Playlists: mapPlaylists(playlists)}, nil
}

func (l *DeezerLoader) LoadHome(ctx context.Context) ([]app.Track, error) {
	tracks, err := l.client.FetchHomeTracks(ctx)
	return mapTracks(tracks), err
}

func (l *DeezerLoader) LoadFlow(ctx context.Context, index int) ([]app.Track, error) {
	tracks, err := l.client.FetchFlowTracks(ctx, index)
	return mapTracks(tracks), err
}

func (l *DeezerLoader) LoadExplore(ctx context.Context) ([]app.Track, error) {
	tracks, err := l.client.FetchExploreTracks(ctx)
	return mapTracks(tracks), err
}

func (l *DeezerLoader) LoadFavorites(ctx context.Context) ([]app.Track, error) {
	tracks, err := l.client.FetchFavoriteTracks(ctx)
	return mapTracks(tracks), err
}

func (l *DeezerLoader) LoadPlaylist(ctx context.Context, playlistID string) ([]app.Track, error) {
	tracks, err := l.client.FetchPlaylistTracks(ctx, playlistID)
	return mapTracks(tracks), err
}

func (l *DeezerLoader) MediaClient() player.MediaClient {
	return l.client
}

func mapTracks(in []deezer.Track) []app.Track {
	out := make([]app.Track, 0, len(in))
	for _, track := range in {
		out = append(out, app.Track{
			ID:     track.ID,
			Title:  track.Title,
			Artist: track.Artist,
		})
	}
	return out
}

func mapPlaylists(in []deezer.Playlist) []app.Playlist {
	out := make([]app.Playlist, 0, len(in))
	for _, playlist := range in {
		out = append(out, app.Playlist{
			ID:    playlist.ID,
			Title: playlist.Title,
		})
	}
	return out
}
