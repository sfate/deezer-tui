package player

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"deezer-tui/internal/deezer"
)

const (
	DefaultDeezerChunkSize = 2048
	DefaultPrebufferBytes  = 512 * 1024
)

type BufferingStage string

const (
	BufferingStageResolving   BufferingStage = "Resolving..."
	BufferingStageDownloading BufferingStage = "Downloading..."
	BufferingStageDecrypting  BufferingStage = "Decrypting..."
	BufferingStagePreparing   BufferingStage = "Preparing..."
	BufferingStageReady       BufferingStage = "Ready"
)

type MediaClient interface {
	FetchAPIToken(ctx context.Context) (string, error)
	FetchTrackMetadata(ctx context.Context, trackID string) (deezer.TrackMetadata, error)
	FetchMediaURL(ctx context.Context, req deezer.MediaRequest) (string, error)
	OpenSignedStream(ctx context.Context, signedURL string) (io.ReadCloser, int64, error)
}

type Backend interface {
	Start(stream io.ReadSeeker, quality deezer.AudioQuality, onFinished func(error)) (Controller, error)
}

type Controller interface {
	Pause()
	Resume()
	Stop()
	SetVolume(v float32)
}

type EventHandler struct {
	OnTrackChanged      func(meta deezer.TrackMetadata, quality deezer.AudioQuality, initialMS uint64)
	OnBufferingProgress func(percent uint8, stage BufferingStage)
	OnPlaybackProgress  func(currentMS, totalMS uint64)
	OnPlaybackStopped   func()
	OnError             func(error)
}

type PipelineOptions struct {
	PrebufferBytes int
	ChunkSize      int
}

type Session struct {
	mu         sync.RWMutex
	controller Controller
	done       chan error
}

func StartTrackPipeline(
	ctx context.Context,
	client MediaClient,
	backend Backend,
	trackID string,
	quality deezer.AudioQuality,
	seekMS uint64,
	handler EventHandler,
	opts PipelineOptions,
) *Session {
	if opts.PrebufferBytes <= 0 {
		opts.PrebufferBytes = DefaultPrebufferBytes
	}
	if opts.ChunkSize <= 0 {
		opts.ChunkSize = DefaultDeezerChunkSize
	}

	session := &Session{done: make(chan error, 1)}
	go func() {
		session.done <- runTrackPipeline(ctx, client, backend, session, trackID, quality, seekMS, handler, opts)
		close(session.done)
	}()
	return session
}

func (s *Session) Pause()              { s.withController(func(c Controller) { c.Pause() }) }
func (s *Session) Resume()             { s.withController(func(c Controller) { c.Resume() }) }
func (s *Session) Stop()               { s.withController(func(c Controller) { c.Stop() }) }
func (s *Session) SetVolume(v float32) { s.withController(func(c Controller) { c.SetVolume(v) }) }

func (s *Session) FadeOutStop(duration time.Duration) {
	s.mu.RLock()
	controller := s.controller
	s.mu.RUnlock()
	if controller == nil {
		return
	}

	steps := 20
	stepDuration := duration / time.Duration(steps)
	if stepDuration <= 0 {
		stepDuration = time.Millisecond
	}
	for step := steps - 1; step >= 0; step-- {
		factor := float32(step) / float32(steps)
		controller.SetVolume(factor)
		time.Sleep(stepDuration)
	}
	controller.Stop()
}

func (s *Session) Wait() error {
	err, ok := <-s.done
	if !ok {
		return nil
	}
	return err
}

func (s *Session) setController(c Controller) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.controller = c
}

func (s *Session) withController(fn func(Controller)) {
	s.mu.RLock()
	controller := s.controller
	s.mu.RUnlock()
	if controller != nil {
		fn(controller)
	}
}

func runTrackPipeline(
	ctx context.Context,
	client MediaClient,
	backend Backend,
	session *Session,
	trackID string,
	quality deezer.AudioQuality,
	seekMS uint64,
	handler EventHandler,
	opts PipelineOptions,
) error {
	if _, err := client.FetchAPIToken(ctx); err != nil {
		return reportErr(handler, err)
	}

	metadata, err := client.FetchTrackMetadata(ctx, trackID)
	if err != nil {
		return reportErr(handler, err)
	}

	initialMS := seekMS
	if quality == deezer.AudioQualityFlac {
		initialMS = 0
	}
	if handler.OnTrackChanged != nil {
		handler.OnTrackChanged(metadata, quality, initialMS)
	}

	signedURL, err := client.FetchMediaURL(ctx, deezer.MediaRequest{
		TrackToken: metadata.TrackToken,
		Quality:    quality,
	})
	if err != nil {
		return reportErr(handler, err)
	}

	stream, contentLength, err := client.OpenSignedStream(ctx, signedURL)
	if err != nil {
		return reportErr(handler, err)
	}
	defer func() { _ = stream.Close() }()

	trackKey := deezer.DeriveBlowfishKey(metadata.ID)
	trackDurationMS := uint64(0)
	if metadata.DurationSecs != nil {
		trackDurationMS = *metadata.DurationSecs * 1000
	} else {
		trackDurationMS = estimateTotalDurationMS(contentLength, quality)
	}
	if trackDurationMS == 0 {
		trackDurationMS = 1
	}

	effectiveSeekMS := seekMS
	if quality == deezer.AudioQualityFlac {
		effectiveSeekMS = 0
	}
	if effectiveSeekMS >= trackDurationMS {
		effectiveSeekMS = trackDurationMS - 1
	}

	var seekTargetBytes int64
	if contentLength > 0 {
		seekTargetBytes = contentLength * int64(effectiveSeekMS) / int64(trackDurationMS)
	}

	if handler.OnPlaybackProgress != nil {
		handler.OnPlaybackProgress(effectiveSeekMS, trackDurationMS)
	}

	ch := make(chan []byte, 128)
	buffer := NewStreamBuffer(ch)

	var started bool
	finishedCh := make(chan error, 1)
	startBackend := func() error {
		if started {
			return nil
		}
		started = true
		controller, err := backend.Start(buffer, quality, func(playErr error) {
			select {
			case finishedCh <- playErr:
			default:
			}
			if playErr == nil && handler.OnPlaybackStopped != nil {
				handler.OnPlaybackStopped()
			}
		})
		if err != nil {
			return err
		}
		session.setController(controller)
		return nil
	}

	var queuedBytes int
	prebufferTarget := opts.PrebufferBytes
	if contentLength > 0 {
		remaining := int(contentLength - seekTargetBytes)
		if remaining > 0 && remaining < prebufferTarget {
			prebufferTarget = remaining
		}
	}
	reportBufferingProgress(handler, 0, prebufferTarget, BufferingStageDownloading)
	var skippedBytes int64
	pending := make([]byte, 0, opts.ChunkSize*2)
	chunkIndex := 0
	readBuf := make([]byte, 32*1024)

	for {
		n, readErr := stream.Read(readBuf)
		if n > 0 {
			pending = append(pending, readBuf[:n]...)
		}

		for len(pending) >= opts.ChunkSize {
			chunk := append([]byte(nil), pending[:opts.ChunkSize]...)
			pending = pending[opts.ChunkSize:]

			if err := deezer.DecryptChunkInPlaceWithKey(trackKey[:], chunkIndex, chunk); err != nil {
				close(ch)
				return reportErr(handler, err)
			}

			if skippedBytes < seekTargetBytes {
				remainingSkip := int(seekTargetBytes - skippedBytes)
				if remainingSkip >= len(chunk) {
					skippedBytes += int64(len(chunk))
					chunkIndex++
					continue
				}
				chunk = chunk[remainingSkip:]
				skippedBytes = seekTargetBytes
			}

			queuedBytes += len(chunk)
			reportBufferingProgress(handler, queuedBytes, prebufferTarget, BufferingStageDownloading)
			select {
			case ch <- chunk:
			case <-ctx.Done():
				close(ch)
				return ctx.Err()
			}

			if !started && queuedBytes >= opts.PrebufferBytes {
				reportBufferingProgress(handler, prebufferTarget, prebufferTarget, BufferingStageReady)
				if err := startBackend(); err != nil {
					close(ch)
					return reportErr(handler, err)
				}
			}

			chunkIndex++
		}

		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			close(ch)
			return reportErr(handler, readErr)
		}
	}

	if len(pending) > 0 {
		tail := append([]byte(nil), pending...)
		if err := deezer.DecryptChunkInPlaceWithKey(trackKey[:], chunkIndex, tail); err != nil {
			close(ch)
			return reportErr(handler, err)
		}
		if skippedBytes < seekTargetBytes {
			remainingSkip := int(seekTargetBytes - skippedBytes)
			if remainingSkip >= len(tail) {
				close(ch)
				if !started {
					return startBackend()
				}
				return nil
			}
			tail = tail[remainingSkip:]
		}
		select {
		case ch <- tail:
		case <-ctx.Done():
			close(ch)
			return ctx.Err()
		}
		queuedBytes += len(tail)
		reportBufferingProgress(handler, queuedBytes, prebufferTarget, BufferingStageDownloading)
	}

	if !started {
		reportBufferingProgress(handler, prebufferTarget, prebufferTarget, BufferingStageReady)
		if err := startBackend(); err != nil {
			close(ch)
			return reportErr(handler, err)
		}
	}

	close(ch)
	if started {
		select {
		case playErr := <-finishedCh:
			if playErr != nil {
				return reportErr(handler, playErr)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func reportBufferingProgress(handler EventHandler, current, total int, stage BufferingStage) {
	if handler.OnBufferingProgress == nil || total <= 0 {
		return
	}
	percent := current * 100 / total
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	handler.OnBufferingProgress(uint8(percent), stage)
}

func estimateTotalDurationMS(totalBytes int64, quality deezer.AudioQuality) uint64 {
	var bytesPerSec uint64
	switch quality {
	case deezer.AudioQuality128:
		bytesPerSec = 16_000
	case deezer.AudioQuality320:
		bytesPerSec = 40_000
	case deezer.AudioQualityFlac:
		bytesPerSec = 90_000
	default:
		bytesPerSec = 40_000
	}

	if totalBytes <= 0 {
		return 224_000
	}
	duration := uint64(totalBytes) * 1000 / bytesPerSec
	if duration == 0 {
		return 1
	}
	return duration
}

func reportErr(handler EventHandler, err error) error {
	if handler.OnError != nil {
		handler.OnError(err)
	}
	return err
}
