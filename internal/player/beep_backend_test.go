package player

import (
	"context"
	"errors"
	"testing"

	"github.com/faiface/beep"
	"github.com/faiface/beep/effects"
)

type fakeSampleStream struct {
	length   int
	position int
}

func (s *fakeSampleStream) Stream(samples [][2]float64) (int, bool) {
	if s.position >= s.length {
		return 0, false
	}
	n := min(len(samples), s.length-s.position)
	s.position += n
	return n, true
}

func (*fakeSampleStream) Err() error      { return nil }
func (s *fakeSampleStream) Len() int      { return s.length }
func (s *fakeSampleStream) Position() int { return s.position }
func (s *fakeSampleStream) Seek(position int) error {
	if position < 0 || position > s.length {
		return errors.New("sample seek out of range")
	}
	s.position = position
	return nil
}
func (*fakeSampleStream) Close() error { return nil }

func TestSeekDecodedStreamUsesSampleRateAndTime(t *testing.T) {
	stream := &fakeSampleStream{length: 240_000}
	format := beep.Format{SampleRate: beep.SampleRate(48_000)}

	if err := seekDecodedStream(stream, format, 1500); err != nil {
		t.Fatalf("seek decoded stream: %v", err)
	}
	if got, want := stream.Position(), 72_000; got != want {
		t.Fatalf("sample position = %d, want %d", got, want)
	}
}

func TestSeekDecodedStreamZeroTimeDoesNotMove(t *testing.T) {
	stream := &fakeSampleStream{length: 100, position: 12}
	if err := seekDecodedStream(stream, beep.Format{SampleRate: 48_000}, 0); err != nil {
		t.Fatalf("seek decoded stream: %v", err)
	}
	if stream.Position() != 12 {
		t.Fatalf("zero seek moved position to %d", stream.Position())
	}
}

type fakeCloser struct {
	closed bool
}

func (f *fakeCloser) Close() error {
	f.closed = true
	return nil
}

func TestBeepControllerStopReportsCancellation(t *testing.T) {
	closer := &fakeCloser{}
	var got error
	controller := &beepController{
		ctrl:   &beep.Ctrl{},
		volume: &effects.Volume{},
		closer: closer,
		onFinished: func(err error) {
			got = err
		},
	}

	controller.stop(context.Canceled)

	if !closer.closed {
		t.Fatal("expected closer to be closed")
	}
	if !errors.Is(got, context.Canceled) {
		t.Fatalf("expected context.Canceled callback, got %v", got)
	}
}
