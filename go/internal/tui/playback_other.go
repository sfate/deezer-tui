//go:build !darwin

package tui

import (
	"context"

	"deezer-tui-go/internal/deezer"
	"deezer-tui-go/internal/player"
)

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
		backend: player.NewProcessBackend(),
	}
}

func (r *defaultPlayerRuntime) Start(trackID string, quality deezer.AudioQuality, seekMS uint64, handler player.EventHandler) (PlaybackSession, error) {
	session := player.StartTrackPipeline(
		context.Background(),
		r.client,
		r.backend,
		trackID,
		quality,
		seekMS,
		handler,
		player.PipelineOptions{},
	)
	return session, nil
}
