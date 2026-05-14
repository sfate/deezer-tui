package player

import (
	"testing"
	"time"

	"github.com/faiface/beep"
)

type testSampleStreamer struct {
	remaining int
}

func (s *testSampleStreamer) Stream(samples [][2]float64) (int, bool) {
	if s.remaining <= 0 {
		return 0, false
	}
	n := min(len(samples), s.remaining)
	for i := 0; i < n; i++ {
		samples[i] = [2]float64{0.5, 0.5}
	}
	s.remaining -= n
	return n, s.remaining > 0
}

func (s *testSampleStreamer) Err() error { return nil }

var _ beep.Streamer = (*testSampleStreamer)(nil)

func TestVisualizerStreamerAnalyzesBandsAsynchronously(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	streamer := &VisualizerStreamer{
		Streamer:   &testSampleStreamer{remaining: visualizerWindowSize},
		SampleRate: defaultSpeakerSampleRate,
		OnBands: func([]uint8) {
			entered <- struct{}{}
			<-release
		},
	}
	defer streamer.Close()

	done := make(chan struct{})
	go func() {
		buf := make([][2]float64, visualizerWindowSize)
		streamer.Stream(buf)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected Stream to return without waiting for band analysis")
	}

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("expected asynchronous band analysis to run")
	}
	close(release)
}
