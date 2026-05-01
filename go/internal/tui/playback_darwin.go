//go:build darwin

package tui

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"deezer-tui-go/internal/deezer"
	"deezer-tui-go/internal/player"
)

//go:embed mac_player_helper.swift
var macPlayerHelperSource string

type defaultPlayerRuntime struct {
	client    *deezer.Client
	manager   *darwinHelperManager
	prebuffer *darwinPrebufferStore
}

func newPlayerRuntime(loader Loader) PlayerRuntime {
	capable, ok := loader.(playbackCapableLoader)
	if !ok {
		return nil
	}
	return &defaultPlayerRuntime{
		client:    capable.DeezerClient(),
		manager:   newDarwinHelperManager(),
		prebuffer: newDarwinPrebufferStore(),
	}
}

func (r *defaultPlayerRuntime) Start(trackID string, quality deezer.AudioQuality, handler player.EventHandler) (PlaybackSession, error) {
	session := &darwinPlaybackSession{
		done:    make(chan error, 1),
		volume:  1,
		manager: r.manager,
	}
	go session.run(r.client, r.prebuffer, trackID, quality, handler)
	return session, nil
}

func (r *defaultPlayerRuntime) Prebuffer(trackID string, quality deezer.AudioQuality) {
	if r.prebuffer == nil {
		return
	}
	r.prebuffer.Prebuffer(context.Background(), r.client, trackID, quality)
}

type darwinPlaybackSession struct {
	mu       sync.Mutex
	file     string
	paused   bool
	stopped  bool
	volume   float32
	done     chan error
	token    int
	manager  *darwinHelperManager
	finished bool
}

func (s *darwinPlaybackSession) run(client *deezer.Client, prebuffer *darwinPrebufferStore, trackID string, quality deezer.AudioQuality, handler player.EventHandler) {
	defer close(s.done)

	ctx := context.Background()
	prepared, err := prebuffer.TakeOrPrepare(ctx, client, trackID, quality, handler.OnBufferingProgress)
	if err != nil {
		s.done <- err
		return
	}
	defer func() {
		if prepared.File != "" {
			_ = os.Remove(prepared.File)
		}
	}()

	meta := prepared.Meta
	if handler.OnTrackChanged != nil {
		handler.OnTrackChanged(meta, quality, 0)
	}
	s.file = prepared.File

	token, finishCh, err := s.manager.play(s.file, s.volume)
	if err != nil {
		s.done <- err
		return
	}
	s.mu.Lock()
	s.token = token
	paused := s.paused
	stopped := s.stopped
	s.mu.Unlock()

	if stopped {
		_ = s.manager.stop(token)
		s.done <- context.Canceled
		return
	}
	if paused {
		_ = s.manager.pause(token)
	}

	if handler.OnPlaybackProgress != nil {
		total := uint64(0)
		if meta.DurationSecs != nil {
			total = *meta.DurationSecs * 1000
		}
		handler.OnPlaybackProgress(0, total)
	}

	err = <-finishCh
	s.mu.Lock()
	stopped = s.stopped
	s.finished = true
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
	if s.token != 0 {
		_ = s.manager.pause(s.token)
	}
}

func (s *darwinPlaybackSession) Resume() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = false
	if s.token != 0 {
		_ = s.manager.resume(s.token)
	}
}

func (s *darwinPlaybackSession) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	if s.finished {
		return
	}
	if s.token != 0 {
		_ = s.manager.stop(s.token)
	}
}

func (s *darwinPlaybackSession) FadeOutStop(duration time.Duration) {
	if duration <= 0 {
		s.Stop()
		return
	}

	steps := 20
	stepDuration := duration / time.Duration(steps)
	if stepDuration <= 0 {
		stepDuration = time.Millisecond
	}
	for step := steps - 1; step >= 0; step-- {
		s.SetVolume(float32(step) / float32(steps))
		time.Sleep(stepDuration)
	}
	s.Stop()
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
	if s.token != 0 {
		_ = s.manager.setVolume(s.token, v)
	}
}

type darwinHelperManager struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	waitErrCh chan error
	nextToken int
	current   *darwinPlayHandle
}

type darwinPlayHandle struct {
	token int
	done  chan error
}

func newDarwinHelperManager() *darwinHelperManager {
	return &darwinHelperManager{
		nextToken: 1,
	}
}

func (m *darwinHelperManager) play(path string, volume float32) (int, <-chan error, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureStartedLocked(); err != nil {
		return 0, nil, err
	}

	if m.current != nil {
		select {
		case m.current.done <- context.Canceled:
		default:
		}
	}

	token := m.nextToken
	m.nextToken++
	handle := &darwinPlayHandle{
		token: token,
		done:  make(chan error, 1),
	}
	m.current = handle
	if err := m.sendLocked(fmt.Sprintf("play\t%d\t%.4f\t%s", token, volume, path)); err != nil {
		return 0, nil, err
	}
	return token, handle.done, nil
}

func (m *darwinHelperManager) pause(token int) error {
	return m.simpleCommand(token, "pause")
}

func (m *darwinHelperManager) resume(token int) error {
	return m.simpleCommand(token, "resume")
}

func (m *darwinHelperManager) stop(token int) error {
	return m.simpleCommand(token, "stop")
}

func (m *darwinHelperManager) setVolume(token int, volume float32) error {
	return m.simpleCommand(token, "volume\t"+strconv.FormatFloat(float64(volume), 'f', 4, 32))
}

func (m *darwinHelperManager) simpleCommand(token int, cmd string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureStartedLocked(); err != nil {
		return err
	}
	if m.current == nil || m.current.token != token {
		return nil
	}
	return m.sendLocked(cmd)
}

func (m *darwinHelperManager) ensureStartedLocked() error {
	if m.stdin != nil && m.cmd != nil && m.cmd.Process != nil {
		return nil
	}

	helperPath, err := ensureMacPlayerHelper()
	if err != nil {
		return err
	}

	cmd := exec.Command(helperPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	m.cmd = cmd
	m.stdin = stdin
	m.waitErrCh = make(chan error, 1)
	go m.readLoop(stdout)
	go m.stderrLoop(stderr)
	go func() {
		m.waitErrCh <- cmd.Wait()
		close(m.waitErrCh)
	}()
	return nil
}

func (m *darwinHelperManager) sendLocked(line string) error {
	if m.stdin == nil {
		return errors.New("mac helper stdin is not available")
	}
	_, err := io.WriteString(m.stdin, line+"\n")
	return err
}

func (m *darwinHelperManager) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		switch parts[0] {
		case "finished":
			token, _ := strconv.Atoi(parts[1])
			m.finishToken(token, nil)
		case "stopped":
			token, _ := strconv.Atoi(parts[1])
			m.finishToken(token, context.Canceled)
		case "error":
			token, _ := strconv.Atoi(parts[1])
			msg := "mac player helper error"
			if len(parts) > 2 {
				msg = parts[2]
			}
			m.finishToken(token, errors.New(msg))
		}
	}
	if err := scanner.Err(); err != nil {
		m.finishCurrent(err)
	}
}

func (m *darwinHelperManager) stderrLoop(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		msg := strings.TrimSpace(scanner.Text())
		if msg != "" {
			m.finishCurrent(errors.New(msg))
		}
	}
}

func (m *darwinHelperManager) finishToken(token int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil || m.current.token != token {
		return
	}
	select {
	case m.current.done <- err:
	default:
	}
	m.current = nil
}

func (m *darwinHelperManager) finishCurrent(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return
	}
	select {
	case m.current.done <- err:
	default:
	}
	m.current = nil
}

type darwinPreparedTrack struct {
	TrackID string
	Quality deezer.AudioQuality
	Meta    deezer.TrackMetadata
	File    string
}

type darwinPrebufferJob struct {
	trackID string
	quality deezer.AudioQuality
	done    chan struct{}
	result  *darwinPreparedTrack
	err     error
	cancel  context.CancelFunc
}

type darwinPrebufferStore struct {
	mu       sync.Mutex
	current  *darwinPreparedTrack
	inflight *darwinPrebufferJob
}

func newDarwinPrebufferStore() *darwinPrebufferStore {
	return &darwinPrebufferStore{}
}

func (s *darwinPrebufferStore) Prebuffer(ctx context.Context, client *deezer.Client, trackID string, quality deezer.AudioQuality) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(trackID) == "" {
		s.clearLocked()
		return
	}
	if s.current != nil && s.current.TrackID == trackID && s.current.Quality == quality {
		return
	}
	if s.inflight != nil && s.inflight.trackID == trackID && s.inflight.quality == quality {
		return
	}

	s.clearLocked()
	jobCtx, cancel := context.WithCancel(ctx)
	job := &darwinPrebufferJob{
		trackID: trackID,
		quality: quality,
		done:    make(chan struct{}),
		cancel:  cancel,
	}
	s.inflight = job
	go s.runJob(jobCtx, client, job)
}

func (s *darwinPrebufferStore) TakeOrPrepare(ctx context.Context, client *deezer.Client, trackID string, quality deezer.AudioQuality, onBufferingProgress func(uint8)) (*darwinPreparedTrack, error) {
	for {
		s.mu.Lock()
		if s.current != nil && s.current.TrackID == trackID && s.current.Quality == quality {
			prepared := s.current
			s.current = nil
			s.mu.Unlock()
			if onBufferingProgress != nil {
				onBufferingProgress(100)
			}
			return prepared, nil
		}
		if s.current != nil && (s.current.TrackID != trackID || s.current.Quality != quality) {
			if s.current.File != "" {
				_ = os.Remove(s.current.File)
			}
			s.current = nil
		}
		if s.inflight != nil && s.inflight.trackID == trackID && s.inflight.quality == quality {
			job := s.inflight
			s.mu.Unlock()
			if onBufferingProgress != nil {
				onBufferingProgress(80)
			}
			<-job.done
			if job.err != nil {
				return nil, job.err
			}
			continue
		}
		if s.inflight != nil && (s.inflight.trackID != trackID || s.inflight.quality != quality) {
			s.inflight.cancel()
			s.inflight = nil
		}
		s.mu.Unlock()
		prepared, err := prepareDarwinTrackFile(ctx, client, trackID, quality, onBufferingProgress)
		if err != nil {
			return nil, err
		}
		return prepared, nil
	}
}

func (s *darwinPrebufferStore) runJob(ctx context.Context, client *deezer.Client, job *darwinPrebufferJob) {
	defer close(job.done)

	result, err := prepareDarwinTrackFile(ctx, client, job.trackID, job.quality, nil)
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.inflight != job {
		if result != nil && result.File != "" {
			_ = os.Remove(result.File)
		}
		return
	}
	s.inflight = nil
	job.err = err
	job.result = result
	if err != nil {
		return
	}
	if s.current != nil && s.current.File != "" {
		_ = os.Remove(s.current.File)
	}
	s.current = result
}

func (s *darwinPrebufferStore) clearLocked() {
	if s.inflight != nil {
		s.inflight.cancel()
		s.inflight = nil
	}
	if s.current != nil && s.current.File != "" {
		_ = os.Remove(s.current.File)
	}
	s.current = nil
}

func prepareDarwinTrackFile(ctx context.Context, client *deezer.Client, trackID string, quality deezer.AudioQuality, onBufferingProgress func(uint8)) (*darwinPreparedTrack, error) {
	if onBufferingProgress != nil {
		onBufferingProgress(0)
	}
	if _, err := client.FetchAPIToken(ctx); err != nil {
		return nil, err
	}
	if onBufferingProgress != nil {
		onBufferingProgress(10)
	}

	meta, err := client.FetchTrackMetadata(ctx, trackID)
	if err != nil {
		return nil, err
	}
	if onBufferingProgress != nil {
		onBufferingProgress(25)
	}

	signedURL, err := client.FetchMediaURL(ctx, deezer.MediaRequest{
		TrackToken: meta.TrackToken,
		Quality:    quality,
	})
	if err != nil {
		return nil, err
	}
	if onBufferingProgress != nil {
		onBufferingProgress(40)
	}

	encrypted, err := client.FetchEncryptedBytesFromSignedURLWithProgress(ctx, signedURL, func(downloaded, total int64) {
		if onBufferingProgress == nil {
			return
		}
		if total <= 0 {
			onBufferingProgress(40)
			return
		}
		percent := 40 + int((downloaded*40)/total)
		if percent > 80 {
			percent = 80
		}
		onBufferingProgress(uint8(percent))
	})
	if err != nil {
		return nil, err
	}
	if onBufferingProgress != nil {
		onBufferingProgress(80)
	}

	decrypted, err := deezer.DecryptAudioStream(meta.ID, encrypted)
	if err != nil {
		return nil, err
	}
	if onBufferingProgress != nil {
		onBufferingProgress(90)
	}

	file, err := os.CreateTemp("", "deezer-tui-*"+qualityExtension(quality))
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(decrypted); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return nil, err
	}
	if onBufferingProgress != nil {
		onBufferingProgress(100)
	}

	return &darwinPreparedTrack{
		TrackID: trackID,
		Quality: quality,
		Meta:    meta,
		File:    file.Name(),
	}, nil
}

func qualityExtension(quality deezer.AudioQuality) string {
	switch quality {
	case deezer.AudioQualityFlac:
		return ".flac"
	default:
		return ".mp3"
	}
}

var (
	macHelperOnce sync.Once
	macHelperPath string
	macHelperErr  error
)

func ensureMacPlayerHelper() (string, error) {
	macHelperOnce.Do(func() {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			macHelperErr = err
			return
		}
		dir := filepath.Join(cacheDir, "deezer-tui-go")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			macHelperErr = err
			return
		}
		srcPath := filepath.Join(dir, "mac_player_helper.swift")
		binPath := filepath.Join(dir, "mac-player-helper")
		sumPath := filepath.Join(dir, "mac-player-helper.sha256")
		sourceSum := fmt.Sprintf("%x", sha256.Sum256([]byte(macPlayerHelperSource)))
		if info, err := os.Stat(binPath); err == nil && !info.IsDir() {
			if existingSum, err := os.ReadFile(sumPath); err == nil && string(existingSum) == sourceSum {
				macHelperPath = binPath
				return
			}
		}
		if err := os.WriteFile(srcPath, []byte(macPlayerHelperSource), 0o644); err != nil {
			macHelperErr = err
			return
		}
		cmd := exec.Command("/usr/bin/xcrun", "swiftc", "-O", "-o", binPath, srcPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			macHelperErr = fmt.Errorf("compile mac player helper: %w: %s", err, string(output))
			return
		}
		if err := os.WriteFile(sumPath, []byte(sourceSum), 0o644); err != nil {
			macHelperErr = err
			return
		}
		macHelperPath = binPath
	})
	if macHelperErr != nil {
		return "", macHelperErr
	}
	return macHelperPath, nil
}
