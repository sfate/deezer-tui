package player

import (
	"math"
	"os"
	"sync"
	"time"

	"deezer-tui/internal/deezer"
	"github.com/faiface/beep"
)

const (
	visualizerBands      = 8
	visualizerWindowSize = 1024
	visualizerMinPeriod  = 65 * time.Millisecond
)

var visualizerFrequencies = [visualizerBands]float64{63, 125, 250, 500, 1000, 2000, 4000, 8000}

type VisualizerStreamer struct {
	Streamer   beep.Streamer
	SampleRate beep.SampleRate
	OnBands    func([]uint8)

	mu       sync.Mutex
	analyzer *asyncVisualizerAnalyzer
	closed   bool
}

func (v *VisualizerStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := v.Streamer.Stream(samples)
	if n > 0 && v.OnBands != nil {
		if analyzer := v.asyncAnalyzer(); analyzer != nil {
			analyzer.add(samples[:n])
		}
	}
	return n, ok
}

func (v *VisualizerStreamer) Err() error {
	if errStreamer, ok := v.Streamer.(interface{ Err() error }); ok {
		return errStreamer.Err()
	}
	return nil
}

func (v *VisualizerStreamer) Close() {
	v.mu.Lock()
	v.closed = true
	analyzer := v.analyzer
	v.analyzer = nil
	v.mu.Unlock()
	if analyzer != nil {
		analyzer.close()
	}
}

func (v *VisualizerStreamer) asyncAnalyzer() *asyncVisualizerAnalyzer {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return nil
	}
	if v.analyzer == nil {
		v.analyzer = newAsyncVisualizerAnalyzer(float64(v.SampleRate), v.OnBands)
	}
	return v.analyzer
}

func StreamVisualizerFile(path string, quality deezer.AudioQuality, startMS uint64, stop <-chan struct{}, paused <-chan bool, onBands func([]uint8)) {
	if onBands == nil || path == "" {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()

	streamer, format, err := decodeStream(file, quality)
	if err != nil {
		return
	}
	defer func() { _ = streamer.Close() }()

	skip := format.SampleRate.N(time.Duration(startMS) * time.Millisecond)
	if skip > 0 {
		if seeker, ok := streamer.(beep.StreamSeeker); ok {
			_ = seeker.Seek(skip)
		} else {
			discardSamples(streamer, skip)
		}
	}

	analyzer := visualizerAnalyzer{sampleRate: float64(format.SampleRate), onBands: onBands}
	buf := make([][2]float64, visualizerWindowSize)
	isPaused := false
	for {
		select {
		case <-stop:
			return
		case isPaused = <-paused:
			continue
		default:
		}
		if isPaused {
			select {
			case <-stop:
				return
			case isPaused = <-paused:
			}
			continue
		}
		start := time.Now()
		n, ok := streamer.Stream(buf)
		if n > 0 {
			analyzer.add(buf[:n])
		}
		if !ok {
			return
		}
		elapsed := time.Since(start)
		windowDuration := time.Duration(float64(time.Second) * float64(n) / float64(format.SampleRate))
		if windowDuration > elapsed {
			timer := time.NewTimer(windowDuration - elapsed)
			select {
			case <-stop:
				timer.Stop()
				return
			case isPaused = <-paused:
				timer.Stop()
			case <-timer.C:
			}
		}
	}
}

type visualizerAnalyzer struct {
	sampleRate float64
	onBands    func([]uint8)
	window     []float64
	lastEmit   time.Time
}

func (a *visualizerAnalyzer) add(samples [][2]float64) {
	for _, sample := range samples {
		a.window = append(a.window, (sample[0]+sample[1])*0.5)
		for len(a.window) >= visualizerWindowSize {
			window := a.window[:visualizerWindowSize]
			a.window = a.window[visualizerWindowSize:]
			if time.Since(a.lastEmit) < visualizerMinPeriod {
				continue
			}
			a.lastEmit = time.Now()
			a.onBands(analyzeBands(window, a.sampleRate))
		}
	}
}

type asyncVisualizerAnalyzer struct {
	sampleRate float64
	onBands    func([]uint8)
	windows    chan []float64
	done       chan struct{}
	once       sync.Once
	window     []float64
	lastEmit   time.Time
}

func newAsyncVisualizerAnalyzer(sampleRate float64, onBands func([]uint8)) *asyncVisualizerAnalyzer {
	a := &asyncVisualizerAnalyzer{
		sampleRate: sampleRate,
		onBands:    onBands,
		windows:    make(chan []float64, 1),
		done:       make(chan struct{}),
	}
	go a.run()
	return a
}

func (a *asyncVisualizerAnalyzer) add(samples [][2]float64) {
	for _, sample := range samples {
		a.window = append(a.window, (sample[0]+sample[1])*0.5)
		for len(a.window) >= visualizerWindowSize {
			window := append([]float64(nil), a.window[:visualizerWindowSize]...)
			a.window = a.window[visualizerWindowSize:]
			if time.Since(a.lastEmit) < visualizerMinPeriod {
				continue
			}
			a.lastEmit = time.Now()
			select {
			case a.windows <- window:
			default:
			}
		}
	}
}

func (a *asyncVisualizerAnalyzer) run() {
	for {
		select {
		case <-a.done:
			return
		case window := <-a.windows:
			a.onBands(analyzeBands(window, a.sampleRate))
		}
	}
}

func (a *asyncVisualizerAnalyzer) close() {
	a.once.Do(func() {
		close(a.done)
	})
}

func analyzeBands(samples []float64, sampleRate float64) []uint8 {
	if sampleRate <= 0 {
		sampleRate = float64(defaultSpeakerSampleRate)
	}
	var sumSquares float64
	for _, sample := range samples {
		sumSquares += sample * sample
	}
	rms := math.Sqrt(sumSquares / float64(len(samples)))
	levels := make([]uint8, visualizerBands)
	for i, freq := range visualizerFrequencies {
		power := (goertzelPower(samples, sampleRate, freq*0.75) +
			goertzelPower(samples, sampleRate, freq) +
			goertzelPower(samples, sampleRate, freq*1.5)) / 3
		level := int(math.Sqrt(power)*30 + rms*12)
		if level > 8 {
			level = 8
		}
		if level < 0 {
			level = 0
		}
		levels[i] = uint8(level)
	}
	return levels
}

func goertzelPower(samples []float64, sampleRate, freq float64) float64 {
	k := 0.5 + float64(len(samples))*freq/sampleRate
	omega := 2 * math.Pi * k / float64(len(samples))
	coeff := 2 * math.Cos(omega)
	var q0, q1, q2 float64
	for i, sample := range samples {
		window := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(len(samples)-1))
		q0 = coeff*q1 - q2 + sample*window
		q2 = q1
		q1 = q0
	}
	return (q1*q1 + q2*q2 - q1*q2*coeff) / float64(len(samples)*len(samples))
}

func discardSamples(streamer beep.Streamer, count int) {
	buf := make([][2]float64, visualizerWindowSize)
	for count > 0 {
		n, ok := streamer.Stream(buf[:min(count, len(buf))])
		count -= n
		if !ok || n == 0 {
			return
		}
	}
}
