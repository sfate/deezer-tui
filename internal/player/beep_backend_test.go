package player

import (
	"context"
	"errors"
	"testing"

	"github.com/faiface/beep"
	"github.com/faiface/beep/effects"
)

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
