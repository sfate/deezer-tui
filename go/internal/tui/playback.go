package tui

import (
	"context"

	"deezer-tui-go/internal/deezer"
	"deezer-tui-go/internal/player"
)

type PlaybackSession interface {
	Pause()
	Resume()
	Stop()
	Wait() error
	SetVolume(v float32)
}

type PlayerRuntime interface {
	Start(trackID string, quality deezer.AudioQuality, handler player.EventHandler) (PlaybackSession, error)
}

type playbackCapableLoader interface {
	Loader
	MediaClient() player.MediaClient
}

type defaultPlayerRuntime struct {
	client  player.MediaClient
	backend player.Backend
}

func newPlayerRuntime(loader Loader) PlayerRuntime {
	capable, ok := loader.(playbackCapableLoader)
	if !ok {
		return nil
	}
	return &defaultPlayerRuntime{
		client:  capable.MediaClient(),
		backend: player.NewBeepBackend(),
	}
}

func (r *defaultPlayerRuntime) Start(trackID string, quality deezer.AudioQuality, handler player.EventHandler) (PlaybackSession, error) {
	session := player.StartTrackPipeline(
		context.Background(),
		r.client,
		r.backend,
		trackID,
		quality,
		0,
		handler,
		player.PipelineOptions{},
	)
	return session, nil
}
