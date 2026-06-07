package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joho/godotenv"

	"deerflow-tui/internal/config"
	"deerflow-tui/internal/tui"
)

func main() {
	// Load .env file if it exists (best-effort)
	_ = godotenv.Load()

	if os.Getenv("TERM") == "" {
		_ = os.Setenv("TERM", "xterm-256color")
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "deerflow-tui: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(tui.NewModel(cfg), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "deerflow-tui: %v\n", err)
		os.Exit(1)
	}
}
