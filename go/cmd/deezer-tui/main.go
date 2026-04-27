package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"deezer-tui-go/internal/tui"
)

func main() {
	program := tea.NewProgram(tui.New())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "deezer-tui-go: %v\n", err)
		os.Exit(1)
	}
}
