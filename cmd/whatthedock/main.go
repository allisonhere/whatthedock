package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/demo"
	dockerprovider "github.com/allisonhere/whatthedock/internal/docker"
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

	provider, err := providerForMode(*demoMode || os.Getenv("WHATTHEDOCK_PROVIDER") == "demo")
	if err != nil {
		fmt.Fprintf(os.Stderr, "whatthedock: %v\n", err)
		os.Exit(1)
	}
	settingsPath, settings, err := loadSettings()
	if err != nil {
		fmt.Fprintf(os.Stderr, "whatthedock: %v\n", err)
		os.Exit(1)
	}
	model := ui.NewModelWithSettings(provider, settings, settingsPath)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "whatthedock: %v\n", err)
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

func providerForMode(demoMode bool) (app.Provider, error) {
	if demoMode {
		return demo.NewProvider(), nil
	}
	return dockerprovider.NewLocalProvider()
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
	return path, settings, nil
}
