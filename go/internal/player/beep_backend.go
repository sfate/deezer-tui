package player

import (
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"deezer-tui-go/internal/deezer"
	"github.com/faiface/beep"
	"github.com/faiface/beep/effects"
	"github.com/faiface/beep/flac"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"
)

const (
	defaultSpeakerSampleRate beep.SampleRate = 48000
)

const defaultResampleQuality = 4

type BeepBackend struct {
	mu               sync.Mutex
	initOnce         sync.Once
	initErr          error
	targetSampleRate beep.SampleRate
	bufferDuration   time.Duration
	current          *beepController
}

func NewBeepBackend() *BeepBackend {
	return &BeepBackend{
		targetSampleRate: defaultSpeakerSampleRate,
		bufferDuration:   100 * time.Millisecond,
	}
}

func (b *BeepBackend) Start(stream io.ReadSeeker, quality deezer.AudioQuality, onFinished func(error)) (Controller, error) {
	if err := b.ensureSpeaker(); err != nil {
		return nil, err
	}

	streamer, format, err := decodeStream(stream, quality)
	if err != nil {
		return nil, err
	}

	playback := beep.Streamer(streamer)
	if format.SampleRate != b.targetSampleRate {
		playback = beep.Resample(defaultResampleQuality, format.SampleRate, b.targetSampleRate, playback)
	}

	ctrl := &beep.Ctrl{Streamer: playback}
	vol := &effects.Volume{Streamer: ctrl, Base: 2}
	controller := &beepController{
		ctrl:       ctrl,
		volume:     vol,
		closer:     streamer,
		onFinished: onFinished,
	}

	controller.sequence = beep.Seq(vol, beep.Callback(func() {
		controller.finish(true)
	}))

	b.mu.Lock()
	if b.current != nil {
		b.current.stopWithoutCallback()
	}
	b.current = controller
	b.mu.Unlock()

	speaker.Play(controller.sequence)
	return controller, nil
}

func (b *BeepBackend) ensureSpeaker() error {
	b.initOnce.Do(func() {
		b.initErr = speaker.Init(b.targetSampleRate, b.targetSampleRate.N(b.bufferDuration))
	})
	if b.initErr != nil {
		return fmt.Errorf("initialize speaker backend: %w", b.initErr)
	}
	return nil
}

type beepController struct {
	ctrl       *beep.Ctrl
	volume     *effects.Volume
	closer     io.Closer
	sequence   beep.Streamer
	onFinished func(error)
	finished   atomic.Bool
}

func (c *beepController) Pause() {
	speaker.Lock()
	c.ctrl.Paused = true
	speaker.Unlock()
}

func (c *beepController) Resume() {
	speaker.Lock()
	c.ctrl.Paused = false
	speaker.Unlock()
}

func (c *beepController) Stop() {
	c.stopWithoutCallback()
}

func (c *beepController) SetVolume(v float32) {
	speaker.Lock()
	defer speaker.Unlock()
	if v <= 0 {
		c.volume.Silent = true
		c.volume.Volume = 0
		return
	}
	c.volume.Silent = false
	c.volume.Volume = volumeFactorToExponent(v)
}

func (c *beepController) stopWithoutCallback() {
	if !c.finished.CompareAndSwap(false, true) {
		return
	}

	speaker.Lock()
	c.ctrl.Streamer = nil
	c.ctrl.Paused = false
	c.volume.Silent = true
	speaker.Unlock()

	if c.closer != nil {
		_ = c.closer.Close()
	}
}

func (c *beepController) finish(natural bool) {
	if !c.finished.CompareAndSwap(false, true) {
		return
	}
	if c.closer != nil {
		_ = c.closer.Close()
	}
	if natural && c.onFinished != nil {
		c.onFinished(nil)
	}
}

func decodeStream(stream io.ReadSeeker, quality deezer.AudioQuality) (beep.StreamSeekCloser, beep.Format, error) {
	wrapped := readSeekCloser{ReadSeeker: stream}
	switch quality {
	case deezer.AudioQualityFlac:
		streamer, format, err := flac.Decode(wrapped)
		if err != nil {
			return nil, beep.Format{}, fmt.Errorf("decode FLAC stream: %w", err)
		}
		return streamer, format, nil
	case deezer.AudioQuality128, deezer.AudioQuality320:
		streamer, format, err := mp3.Decode(wrapped)
		if err != nil {
			return nil, beep.Format{}, fmt.Errorf("decode MP3 stream: %w", err)
		}
		return streamer, format, nil
	default:
		return nil, beep.Format{}, fmt.Errorf("unsupported audio quality %q", quality)
	}
}

type readSeekCloser struct {
	io.ReadSeeker
}

func (r readSeekCloser) Close() error { return nil }

func volumeFactorToExponent(v float32) float64 {
	return mathLog2(clampFloat64(float64(v), 0.0001, 4))
}

func clampFloat64(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func mathLog2(v float64) float64 {
	return math.Log(v) / math.Log(2)
}
