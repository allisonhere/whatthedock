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
	provider, systemName, startupErr := providerForMode(context.Background(), *demoMode || os.Getenv("WHATTHEDOCK_PROVIDER") == "demo", settings, factory)
	if startupErr != nil {
		// A configured active system that can't connect (unreachable host,
		// no key-based auth set up yet, ...) used to be fatal here — the app
		// would refuse to even open, which is exactly the wrong failure mode
		// for a config problem the Systems overlay exists to let you fix:
		// you'd be locked out of the one place that could fix it. Fall back
		// to local Docker instead and say why, so the app still launches
		// and the broken system can be repaired (or switched away from)
		// from inside it.
		local := config.DefaultSystem()
		provider, err = factory.Provider(context.Background(), local)
		if err != nil {
			fmt.Fprintf(os.Stderr, "whatthedock: %v\n", err)
			os.Exit(1)
		}
	}
	model := ui.NewModelWithProviderFactory(provider, settings, settingsPath, factory.Provider).WithVersion(version)
	if startupErr != nil {
		model = model.WithStatus("couldn't connect to "+systemName+": "+startupErr.Error()+" — using local Docker instead", true)
	}
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

// providerForMode resolves settings.ActiveSystem to a live provider.
// systemName is the resolved system's display name — always returned
// (even on error) purely so a caller can name it in a fallback message
// without re-deriving config.FindSystem's result itself.
func providerForMode(ctx context.Context, demoMode bool, settings config.Settings, factory systems.Factory) (provider app.Provider, systemName string, err error) {
	if demoMode {
		return demo.NewProvider(), "demo", nil
	}
	settings = config.NormalizeSystems(settings)
	system := config.FindSystem(settings.Systems, settings.ActiveSystem)
	if system == nil {
		local := config.DefaultSystem()
		system = &local
	}
	provider, err = factory.Provider(ctx, *system)
	return provider, system.Name, err
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
