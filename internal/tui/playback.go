package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"deezer-tui/internal/deezer"
	"deezer-tui/internal/player"
)

type PlaybackSession interface {
	Pause()
	Resume()
	Stop()
	Wait() error
	SetVolume(v float32)
}

type PlayerRuntime interface {
	Start(trackID string, quality deezer.AudioQuality, seekMS uint64, handler player.EventHandler) (PlaybackSession, error)
}

type PrebufferStatus int

const (
	PrebufferStatusScheduled PrebufferStatus = iota
	PrebufferStatusLoading
	PrebufferStatusReady
	PrebufferStatusFailed
)

type PrebufferingRuntime interface {
	PlayerRuntime
	Prebuffer(trackIDs []string, quality deezer.AudioQuality, events chan<- tea.Msg)
}

type MediaControlCommandKind int

const (
	MediaControlPlay MediaControlCommandKind = iota
	MediaControlPause
	MediaControlToggle
	MediaControlNext
	MediaControlPrevious
	MediaControlSetPosition
)

type MediaControlCommand struct {
	Kind       MediaControlCommandKind
	PositionMS uint64
}

type MediaControlState struct {
	Playing     bool
	Stopped     bool
	TrackID     string
	Title       string
	Artist      string
	AlbumArtURL string
	PositionMS  uint64
	DurationMS  uint64
	Volume      uint16
	CanNext     bool
	CanPrevious bool
	CanSeek     bool
	RepeatMode  appRepeatMode
}

type appRepeatMode int

type MediaControlRuntime interface {
	PlayerRuntime
	MediaControlEvents() <-chan MediaControlCommand
	UpdateMediaControl(MediaControlState)
}

type fadeStoppingSession interface {
	FadeOutStop(duration time.Duration)
}

type playbackCapableLoader interface {
	Loader
	MediaClient() player.MediaClient
	DeezerClient() *deezer.Client
}
