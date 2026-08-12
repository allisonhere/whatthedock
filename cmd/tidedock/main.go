package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/tidedock/internal/app"
	"github.com/allisonhere/tidedock/internal/demo"
	dockerprovider "github.com/allisonhere/tidedock/internal/docker"
	"github.com/allisonhere/tidedock/internal/ui"
)

func main() {
	demoMode := flag.Bool("demo", false, "run against TideDock's built-in demo Docker environment")
	flag.Parse()

	provider, err := providerForMode(*demoMode || os.Getenv("TIDEDOCK_PROVIDER") == "demo")
	if err != nil {
		fmt.Fprintf(os.Stderr, "tidedock: %v\n", err)
		os.Exit(1)
	}
	model := ui.NewModel(provider)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tidedock: %v\n", err)
		os.Exit(1)
	}
}

func providerForMode(demoMode bool) (app.Provider, error) {
	if demoMode {
		return demo.NewProvider(), nil
	}
	return dockerprovider.NewLocalProvider()
}
