package actions

import (
	"context"

	"github.com/allisonhere/tidedock/internal/app"
	"github.com/allisonhere/tidedock/internal/domain"
)

type ID string

const (
	Refresh        ID = "refresh"
	StartStop      ID = "start-stop-container"
	Restart        ID = "restart-container"
	FocusLogs      ID = "focus-logs"
	ShowProblems   ID = "show-problems"
	ShowStats      ID = "show-stats"
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
		{ID: OpenFilter, Name: "Filter projects and containers", Shortcut: "/", Aliases: []string{"search"}, Enabled: true},
		{ID: OpenLogFilter, Name: "Filter visible logs", Shortcut: "/", Aliases: []string{"logs search", "log search"}, Enabled: hasContainer},
		{ID: OpenHelp, Name: "Show keyboard help", Shortcut: "?", Aliases: []string{"keys"}, Enabled: true},
		{ID: OpenTheme, Name: "Choose theme", Shortcut: "T", Aliases: []string{"themes", "palette", "colors"}, Enabled: true},
		{ID: OpenSettings, Name: "Open settings", Shortcut: ",", Aliases: []string{"preferences", "options", "config"}, Enabled: true},
		{ID: CommandPalette, Name: "Command palette", Shortcut: "ctrl+k", Aliases: []string{"commands"}, Enabled: true},
		{ID: Quit, Name: "Quit TideDock", Shortcut: "q", Aliases: []string{"exit"}, Enabled: true},
	}
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
