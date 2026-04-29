//go:build darwin

package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"deezer-tui-go/internal/deezer"
	"deezer-tui-go/internal/player"
)

type defaultPlayerRuntime struct {
	client *deezer.Client
}

func newPlayerRuntime(loader Loader) PlayerRuntime {
	capable, ok := loader.(playbackCapableLoader)
	if !ok {
		return nil
	}
	return &defaultPlayerRuntime{
		client: capable.DeezerClient(),
	}
}

func (r *defaultPlayerRuntime) Start(trackID string, quality deezer.AudioQuality, handler player.EventHandler) (PlaybackSession, error) {
	session := &darwinPlaybackSession{
		done:   make(chan error, 1),
		volume: 1,
	}

	go session.run(r.client, trackID, quality, handler)
	return session, nil
}

type darwinPlaybackSession struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	file    string
	paused  bool
	stopped bool
	volume  float32
	done    chan error
}

func (s *darwinPlaybackSession) run(client *deezer.Client, trackID string, quality deezer.AudioQuality, handler player.EventHandler) {
	defer close(s.done)

	ctx := context.Background()
	if _, err := client.FetchAPIToken(ctx); err != nil {
		s.done <- err
		return
	}

	meta, err := client.FetchTrackMetadata(ctx, trackID)
	if err != nil {
		s.done <- err
		return
	}
	if handler.OnTrackChanged != nil {
		handler.OnTrackChanged(meta, quality, 0)
	}

	signedURL, err := client.FetchMediaURL(ctx, deezer.MediaRequest{
		TrackToken: meta.TrackToken,
		Quality:    quality,
	})
	if err != nil {
		s.done <- err
		return
	}

	encrypted, err := client.FetchEncryptedBytesFromSignedURL(ctx, signedURL)
	if err != nil {
		s.done <- err
		return
	}

	decrypted, err := deezer.DecryptAudioStream(meta.ID, encrypted)
	if err != nil {
		s.done <- err
		return
	}

	file, err := os.CreateTemp("", "deezer-tui-*"+qualityExtension(quality))
	if err != nil {
		s.done <- err
		return
	}
	defer func() {
		_ = os.Remove(file.Name())
	}()
	s.file = file.Name()

	if _, err := file.Write(decrypted); err != nil {
		_ = file.Close()
		s.done <- err
		return
	}
	if err := file.Close(); err != nil {
		s.done <- err
		return
	}

	s.mu.Lock()
	cmd := exec.Command("/usr/bin/afplay", "-v", fmt.Sprintf("%.2f", s.volume), s.file)
	s.cmd = cmd
	paused := s.paused
	stopped := s.stopped
	s.mu.Unlock()

	if stopped {
		s.done <- context.Canceled
		return
	}

	if err := cmd.Start(); err != nil {
		s.done <- err
		return
	}
	if paused && cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGSTOP)
	}

	if handler.OnPlaybackProgress != nil {
		total := uint64(0)
		if meta.DurationSecs != nil {
			total = *meta.DurationSecs * 1000
		}
		handler.OnPlaybackProgress(0, total)
	}

	err = cmd.Wait()
	s.mu.Lock()
	stopped = s.stopped
	s.mu.Unlock()
	if stopped {
		s.done <- context.Canceled
		return
	}
	if err != nil {
		if handler.OnError != nil {
			handler.OnError(err)
		}
		s.done <- err
		return
	}
	if handler.OnPlaybackStopped != nil {
		handler.OnPlaybackStopped()
	}
	s.done <- nil
}

func (s *darwinPlaybackSession) Pause() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = true
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(syscall.SIGSTOP)
	}
}

func (s *darwinPlaybackSession) Resume() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = false
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(syscall.SIGCONT)
	}
}

func (s *darwinPlaybackSession) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

func (s *darwinPlaybackSession) Wait() error {
	return <-s.done
}

func (s *darwinPlaybackSession) SetVolume(v float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	s.volume = v
}

func qualityExtension(quality deezer.AudioQuality) string {
	switch quality {
	case deezer.AudioQualityFlac:
		return ".flac"
	default:
		return ".mp3"
	}
}
