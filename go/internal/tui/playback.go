package tui

import (
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

type PrebufferingRuntime interface {
	PlayerRuntime
	Prebuffer(trackID string, quality deezer.AudioQuality)
}

type playbackCapableLoader interface {
	Loader
	MediaClient() player.MediaClient
	DeezerClient() *deezer.Client
}
