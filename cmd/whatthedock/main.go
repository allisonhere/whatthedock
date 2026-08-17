package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/demo"
	"github.com/allisonhere/whatthedock/internal/systems"
	"github.com/allisonhere/whatthedock/internal/ui"
)

var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	demoMode := flag.Bool("demo", false, "run against WhatTheDock's built-in demo Docker environment")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(versionString())
		return
	}

	settingsPath, settings, err := loadSettings()
	if err != nil {
		fmt.Fprintf(os.Stderr, "whatthedock: %v\n", err)
		os.Exit(1)
	}
	factory := systems.NewFactory()
	provider, err := providerForMode(context.Background(), *demoMode || os.Getenv("WHATTHEDOCK_PROVIDER") == "demo", settings, factory)
	if err != nil {
		fmt.Fprintf(os.Stderr, "whatthedock: %v\n", err)
		os.Exit(1)
	}
	model := ui.NewModelWithProviderFactory(provider, settings, settingsPath, factory.Provider).WithVersion(version)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := program.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "whatthedock: %v\n", err)
		os.Exit(1)
	}
	restartInto(finalModel)
}

// restartInto re-execs into the binary an in-app update just installed, if
// any (ui.Model.RestartExecPath is empty otherwise). This runs only after
// program.Run() has returned, so Bubble Tea has already restored the
// terminal (alt screen, raw mode) — replacing the process image any
// earlier would replace it out from under that cleanup. syscall.Exec
// replaces the current process rather than spawning a child, so the
// updated binary picks up right where this one left off: same PID, same
// terminal, no visible restart.
func restartInto(finalModel tea.Model) {
	m, ok := finalModel.(ui.Model)
	if !ok {
		return
	}
	exe := m.RestartExecPath()
	if exe == "" {
		return
	}
	if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "whatthedock: update installed but restart failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "whatthedock: run %s again to use it\n", exe)
		os.Exit(1)
	}
}

func versionString() string {
	parts := []string{"whatthedock", version}
	if commit != "" {
		parts = append(parts, "commit "+commit)
	}
	if date != "" {
		parts = append(parts, "built "+date)
	}
	return strings.Join(parts, " ")
}

func providerForMode(ctx context.Context, demoMode bool, settings config.Settings, factory systems.Factory) (app.Provider, error) {
	if demoMode {
		return demo.NewProvider(), nil
	}
	settings = config.NormalizeSystems(settings)
	system := config.FindSystem(settings.Systems, settings.ActiveSystem)
	if system == nil {
		local := config.DefaultSystem()
		system = &local
	}
	return factory.Provider(ctx, *system)
}

func loadSettings() (string, config.Settings, error) {
	path, err := config.SettingsPath()
	if err != nil {
		return "", config.Settings{}, err
	}
	settings, err := config.LoadSettings(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "whatthedock: ignoring settings: %v\n", err)
		return path, config.Settings{}, nil
	}
	return path, config.NormalizeSystems(settings), nil
}
