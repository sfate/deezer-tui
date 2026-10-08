//go:build !darwin && cgo

package player

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"

	"github.com/faiface/beep"
	"github.com/gen2brain/malgo"
	"github.com/sfate/deezer-tui/internal/deezer"
)

const (
	defaultMalgoSampleRate = 48000
	defaultMalgoChannels   = 2
)

type MalgoBackend struct {
	mu       sync.Mutex
	initOnce sync.Once
	initErr  error
	context  *malgo.AllocatedContext
	current  *malgoController
}

func NewMalgoBackend() *MalgoBackend {
	return &MalgoBackend{}
}

func (b *MalgoBackend) Start(stream io.ReadSeeker, quality deezer.AudioQuality, handler EventHandler, onFinished func(error)) (Controller, error) {
	if err := b.ensureContext(); err != nil {
		return nil, err
	}

	streamer, format, err := decodeStream(stream, quality)
	if err != nil {
		return nil, err
	}

	playback := beep.Streamer(streamer)
	if format.SampleRate != beep.SampleRate(defaultMalgoSampleRate) {
		playback = beep.Resample(defaultResampleQuality, format.SampleRate, beep.SampleRate(defaultMalgoSampleRate), playback)
	}
	var visualizer *VisualizerStreamer
	if handler.OnAudioBands != nil {
		visualizer = &VisualizerStreamer{
			Streamer:   playback,
			SampleRate: beep.SampleRate(defaultMalgoSampleRate),
			OnBands:    handler.OnAudioBands,
		}
		playback = visualizer
	}

	controller := &malgoController{
		streamer:   playback,
		closer:     closeVisualizerStream{visualizer: visualizer, closer: streamer},
		onFinished: onFinished,
		volume:     1,
	}

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatF32
	deviceConfig.Playback.Channels = defaultMalgoChannels
	deviceConfig.SampleRate = defaultMalgoSampleRate
	deviceConfig.Alsa.NoMMap = 1
	deviceConfig.Pulse.StreamNamePlayback = "deezer-tui"

	device, err := malgo.InitDevice(b.context.Context, deviceConfig, malgo.DeviceCallbacks{
		Data: controller.fill,
	})
	if err != nil {
		_ = controller.close()
		return nil, fmt.Errorf("initialize malgo playback device: %w", err)
	}
	controller.device = device

	b.mu.Lock()
	if b.current != nil {
		b.current.stopWithoutCallback()
	}
	b.current = controller
	b.mu.Unlock()

	if err := device.Start(); err != nil {
		_ = controller.close()
		return nil, fmt.Errorf("start malgo playback device: %w", err)
	}
	return controller, nil
}

func (b *MalgoBackend) ensureContext() error {
	b.initOnce.Do(func() {
		b.context, b.initErr = malgo.InitContext(nil, malgo.ContextConfig{}, nil)
		if b.initErr != nil {
			b.initErr = fmt.Errorf("initialize malgo context: %w", b.initErr)
		}
	})
	return b.initErr
}

type malgoController struct {
	mu         sync.Mutex
	device     *malgo.Device
	streamer   beep.Streamer
	closer     io.Closer
	onFinished func(error)
	volume     float64
	paused     bool
	stopped    bool
	finished   atomic.Bool
}

func (c *malgoController) Pause() {
	c.mu.Lock()
	c.paused = true
	c.mu.Unlock()
}

func (c *malgoController) Resume() {
	c.mu.Lock()
	c.paused = false
	c.mu.Unlock()
}

func (c *malgoController) Stop() {
	c.stop(context.Canceled, true)
}

func (c *malgoController) SetVolume(v float32) {
	c.mu.Lock()
	c.volume = clampFloat64(float64(v), 0, 1)
	c.mu.Unlock()
}

func (c *malgoController) stopWithoutCallback() {
	c.stop(nil, false)
}

func (c *malgoController) stop(err error, callback bool) {
	if !c.finished.CompareAndSwap(false, true) {
		return
	}

	c.mu.Lock()
	c.stopped = true
	c.mu.Unlock()

	if c.device != nil {
		_ = c.device.Stop()
		c.device.Uninit()
	}
	_ = c.close()
	if callback && c.onFinished != nil {
		c.onFinished(err)
	}
}

func (c *malgoController) finishNatural() {
	if !c.finished.CompareAndSwap(false, true) {
		return
	}
	if c.device != nil {
		_ = c.device.Stop()
		c.device.Uninit()
	}
	_ = c.close()
	if c.onFinished != nil {
		c.onFinished(nil)
	}
}

func (c *malgoController) close() error {
	if c.closer == nil {
		return nil
	}
	return c.closer.Close()
}

func (c *malgoController) fill(output, _ []byte, frameCount uint32) {
	if len(output) == 0 {
		return
	}

	c.mu.Lock()
	if c.paused || c.stopped || c.streamer == nil {
		c.mu.Unlock()
		clear(output)
		return
	}
	volume := c.volume
	samples := make([][2]float64, int(frameCount))
	n, ok := c.streamer.Stream(samples)
	c.mu.Unlock()

	writeFloat32Stereo(output, samples, n, volume)
	if !ok {
		go c.finishNatural()
	}
}

func writeFloat32Stereo(output []byte, samples [][2]float64, n int, volume float64) {
	clear(output)
	sampleCount := min(n, len(samples))
	for i := 0; i < sampleCount; i++ {
		offset := i * defaultMalgoChannels * 4
		if offset+7 >= len(output) {
			return
		}
		left := float32(clampFloat64(samples[i][0]*volume, -1, 1))
		right := float32(clampFloat64(samples[i][1]*volume, -1, 1))
		binary.LittleEndian.PutUint32(output[offset:], math.Float32bits(left))
		binary.LittleEndian.PutUint32(output[offset+4:], math.Float32bits(right))
	}
}
