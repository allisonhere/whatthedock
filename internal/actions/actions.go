package actions

import (
	"context"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/domain"
)

type ID string

const (
	Refresh        ID = "refresh"
	StartStop      ID = "start-stop-container"
	Restart        ID = "restart-container"
	FocusLogs      ID = "focus-logs"
	ShowProblems   ID = "show-problems"
	ShowStats      ID = "show-stats"
	OpenCopy       ID = "open-copy"
	OpenPort       ID = "open-port"
	OpenMount      ID = "open-mount"
	OpenFilter     ID = "open-filter"
	OpenLogFilter  ID = "open-log-filter"
	OpenHelp       ID = "open-help"
	OpenTheme      ID = "open-theme"
	OpenSettings   ID = "open-settings"
	CommandPalette ID = "command-palette"
	Quit           ID = "quit"
)

type Command struct {
	ID       ID
	Name     string
	Shortcut string
	Aliases  []string
	Enabled  bool
	Run      func(context.Context, app.Provider, *domain.Container) error
}

func Catalog(selected *domain.Container) []Command {
	hasContainer := selected != nil
	canStartStop := false
	if selected != nil {
		canStartStop = selected.State == domain.StateRunning || selected.State == domain.StateRestarting || selected.State == domain.StateStopped || selected.State == domain.StateExited
	}
	return []Command{
		{ID: Refresh, Name: "Refresh Docker state", Shortcut: "r", Aliases: []string{"reload"}, Enabled: true},
		{ID: StartStop, Name: "Start or stop selected container", Shortcut: "s", Aliases: []string{"start", "stop"}, Enabled: canStartStop, Run: startStop},
		{ID: Restart, Name: "Restart selected container", Shortcut: "r", Aliases: []string{"bounce"}, Enabled: hasContainer, Run: restart},
		{ID: FocusLogs, Name: "Show logs", Shortcut: "l", Aliases: []string{"tail"}, Enabled: hasContainer},
		{ID: ShowProblems, Name: "Show problems", Shortcut: "p", Aliases: []string{"issues", "health", "unhealthy", "restarting"}, Enabled: true},
		{ID: ShowStats, Name: "Show stats", Shortcut: "g", Aliases: []string{"graphs", "sparklines", "metrics", "cpu", "memory"}, Enabled: true},
		{ID: OpenCopy, Name: "Copy selected detail", Shortcut: "c", Aliases: []string{"clipboard", "copy id", "copy port", "copy label", "copy mount"}, Enabled: hasContainer},
		{ID: OpenPort, Name: "Open selected port", Shortcut: "o", Aliases: []string{"browser", "url", "localhost", "published port"}, Enabled: hasOpenPort(selected)},
		{ID: OpenMount, Name: "Open selected mount", Shortcut: "o", Aliases: []string{"folder", "path", "volume", "bind mount"}, Enabled: hasOpenMount(selected)},
		{ID: OpenFilter, Name: "Filter projects and containers", Shortcut: "/", Aliases: []string{"search"}, Enabled: true},
		{ID: OpenLogFilter, Name: "Filter visible logs", Shortcut: "/", Aliases: []string{"logs search", "log search"}, Enabled: hasContainer},
		{ID: OpenHelp, Name: "Show keyboard help", Shortcut: "?", Aliases: []string{"keys"}, Enabled: true},
		{ID: OpenTheme, Name: "Choose theme", Shortcut: "T", Aliases: []string{"themes", "palette", "colors"}, Enabled: true},
		{ID: OpenSettings, Name: "Open settings", Shortcut: ",", Aliases: []string{"preferences", "options", "config"}, Enabled: true},
		{ID: CommandPalette, Name: "Command palette", Shortcut: "ctrl+k", Aliases: []string{"commands"}, Enabled: true},
		{ID: Quit, Name: "Quit WhatTheDock", Shortcut: "q", Aliases: []string{"exit"}, Enabled: true},
	}
}

func hasOpenPort(selected *domain.Container) bool {
	if selected == nil {
		return false
	}
	for _, port := range selected.Ports {
		if port.Public > 0 {
			return true
		}
	}
	return false
}

func hasOpenMount(selected *domain.Container) bool {
	if selected == nil {
		return false
	}
	for _, mount := range selected.Mounts {
		if mount.Source != "" || mount.Destination != "" {
			return true
		}
	}
	return selected.Compose.ConfigFiles != ""
}

func startStop(ctx context.Context, provider app.Provider, selected *domain.Container) error {
	if selected == nil {
		return nil
	}
	if selected.IsRunning() {
		return provider.StopContainer(ctx, selected.ID)
	}
	return provider.StartContainer(ctx, selected.ID)
}

func restart(ctx context.Context, provider app.Provider, selected *domain.Container) error {
	if selected == nil {
		return nil
	}
	return provider.RestartContainer(ctx, selected.ID)
}
