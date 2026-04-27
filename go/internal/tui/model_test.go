package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"deezer-tui-go/internal/app"
)

func TestViewUsesAltScreen(t *testing.T) {
	model := New()
	model.width = 120
	model.height = 40

	view := model.View()
	if !view.AltScreen {
		t.Fatal("expected alt screen to be enabled")
	}
	if view.WindowTitle != "deezer-tui-go" {
		t.Fatalf("unexpected window title %q", view.WindowTitle)
	}
}

func TestEnterOnFlowLoadsQueueAndMarksFlow(t *testing.T) {
	model := New()
	model.width = 120
	model.height = 40
	model.app.NavState.Select(intPtr(1))
	model.app.ActivePanel = app.ActivePanelNavigation

	nextModel, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "enter"}))
	updated, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", nextModel)
	}

	if updated.app.CurrentPlaylistID == nil || *updated.app.CurrentPlaylistID != "__flow__" {
		t.Fatal("expected flow playlist to be selected")
	}
	if !updated.app.IsFlowQueue {
		t.Fatal("expected flow queue to be active")
	}
	if !updated.app.IsPlaying {
		t.Fatal("expected flow selection to start playback")
	}
	if updated.app.QueueIndex == nil || *updated.app.QueueIndex != 0 {
		t.Fatal("expected queue to start from first track")
	}
	if len(updated.app.QueueTracks) != len(sampleFlowTracks()) {
		t.Fatalf("unexpected queue length %d", len(updated.app.QueueTracks))
	}
}
