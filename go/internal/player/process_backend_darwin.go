package player

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	"deezer-tui-go/internal/deezer"
)

type ProcessBackend struct{}

func NewProcessBackend() *ProcessBackend {
	return &ProcessBackend{}
}

func (b *ProcessBackend) Start(stream io.ReadSeeker, quality deezer.AudioQuality, onFinished func(error)) (Controller, error) {
	file, err := os.CreateTemp("", "deezer-tui-*"+qualityExtension(quality))
	if err != nil {
		return nil, fmt.Errorf("create temp audio file: %w", err)
	}

	controller := &processController{
		filePath:      file.Name(),
		quality:       quality,
		onFinished:    onFinished,
		started:       make(chan struct{}),
		initialVolume: 1,
	}

	go controller.run(file, stream)
	return controller, nil
}

type processController struct {
	mu            sync.Mutex
	cmd           *exec.Cmd
	filePath      string
	quality       deezer.AudioQuality
	onFinished    func(error)
	started       chan struct{}
	doneOnce      sync.Once
	stopped       bool
	paused        bool
	initialVolume float32
}

func (c *processController) run(file *os.File, stream io.ReadSeeker) {
	defer func() {
		_ = file.Close()
		_ = os.Remove(c.filePath)
		c.doneOnce.Do(func() {
			close(c.started)
		})
	}()

	if _, err := io.Copy(file, stream); err != nil {
		c.finish(fmt.Errorf("write temp audio file: %w", err))
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		c.finish(fmt.Errorf("rewind temp audio file: %w", err))
		return
	}

	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	volume := c.initialVolume
	args := []string{"-v", fmt.Sprintf("%.2f", volume), c.filePath}
	cmd := exec.Command("/usr/bin/afplay", args...)
	c.cmd = cmd
	paused := c.paused
	c.mu.Unlock()

	c.doneOnce.Do(func() {
		close(c.started)
	})

	if err := cmd.Start(); err != nil {
		c.finish(fmt.Errorf("start afplay for %s: %w", filepath.Base(c.filePath), err))
		return
	}

	if paused && cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGSTOP)
	}

	if err := cmd.Wait(); err != nil {
		c.mu.Lock()
		stopped := c.stopped
		c.mu.Unlock()
		if !stopped {
			c.finish(fmt.Errorf("afplay exited with error: %w", err))
		}
		return
	}

	c.mu.Lock()
	stopped := c.stopped
	c.mu.Unlock()
	if !stopped {
		c.finish(nil)
	}
}

func (c *processController) Pause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paused = true
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Signal(syscall.SIGSTOP)
	}
}

func (c *processController) Resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paused = false
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Signal(syscall.SIGCONT)
	}
}

func (c *processController) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

func (c *processController) SetVolume(v float32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	c.initialVolume = v
}

func (c *processController) finish(err error) {
	if c.onFinished != nil {
		c.onFinished(err)
	}
}

func qualityExtension(quality deezer.AudioQuality) string {
	switch quality {
	case deezer.AudioQualityFlac:
		return ".flac"
	default:
		return ".mp3"
	}
}
