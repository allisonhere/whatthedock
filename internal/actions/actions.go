package actions

import (
	"context"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/domain"
)

type ID string

const (
	Refresh        ID = "refresh"
	Create         ID = "create-container"
	StartStop      ID = "start-stop-container"
	Restart        ID = "restart-container"
	Delete         ID = "delete-container"
	Replicate      ID = "replicate-container"
	Clone          ID = "clone-container"
	Yank           ID = "yank-container"
	Paste          ID = "paste-container"
	CurateImages   ID = "curate-images"
	CurateNetworks ID = "curate-networks"
	CurateVolumes  ID = "curate-volumes"
	CurateCompose  ID = "curate-compose"
	ExecShell      ID = "exec-shell"
	FocusLogs      ID = "focus-logs"
	ShowProblems   ID = "show-problems"
	ShowStats      ID = "show-stats"
	OpenCopy       ID = "open-copy"
	OpenPort       ID = "open-port"
	OpenMount      ID = "open-mount"
	OpenFilter     ID = "open-filter"
	OpenLogFilter  ID = "open-log-filter"
	OpenHelp       ID = "open-help"
	OpenAbout      ID = "open-about"
	OpenTheme      ID = "open-theme"
	OpenSettings   ID = "open-settings"
	OpenSystems    ID = "open-systems"
	CommandPalette ID = "command-palette"
	ShutdownHost   ID = "shutdown-host"
	RebootHost     ID = "reboot-host"
	Quit           ID = "quit"
)

type Command struct {
	ID       ID
	Name     string
	Shortcut string
	Aliases  []string
	Enabled  bool
	Category string
	Run      func(context.Context, app.Provider, *domain.Container) error
}

func Catalog(selected *domain.Container) []Command {
	hasContainer := selected != nil
	canStartStop := false
	if selected != nil {
		canStartStop = selected.State == domain.StateRunning || selected.State == domain.StateRestarting || selected.State == domain.StateStopped || selected.State == domain.StateExited
	}
	canExec := selected != nil && selected.IsRunning()
	return []Command{
		{ID: Refresh, Name: "Refresh Docker state", Shortcut: "r", Aliases: []string{"reload"}, Enabled: true, Category: "Main"},
		{ID: Create, Name: "Create container or Compose service", Shortcut: "n", Aliases: []string{"new", "run", "compose service"}, Enabled: true, Category: "Container Management"},
		{ID: StartStop, Name: "Start or stop selected container", Shortcut: "s", Aliases: []string{"start", "stop"}, Enabled: canStartStop, Category: "Container Management", Run: startStop},
		{ID: Restart, Name: "Restart selected container", Shortcut: "r", Aliases: []string{"bounce"}, Enabled: hasContainer, Category: "Container Management", Run: restart},
		{ID: Delete, Name: "Delete container or Compose service", Shortcut: "D", Aliases: []string{"remove", "rm", "delete override"}, Enabled: hasContainer, Category: "Container Management"},
		{ID: Replicate, Name: "Replicate: pull latest image and recreate in place", Shortcut: "u", Aliases: []string{"update image", "pull", "recreate"}, Enabled: hasContainer, Category: "Container Management"},
		{ID: Clone, Name: "Clone container or Compose service under a new name", Shortcut: "C", Aliases: []string{"duplicate", "copy container"}, Enabled: hasContainer, Category: "Container Management"},
		{ID: Yank, Name: "Yank container configuration (Container Clipboard)", Shortcut: "y", Aliases: []string{"clipboard", "copy container config", "migrate"}, Enabled: hasContainer, Category: "Container Management"},
		{ID: Paste, Name: "Paste yanked container onto this host (Container Clipboard)", Shortcut: "P", Aliases: []string{"clipboard", "deploy yanked", "migrate"}, Enabled: true, Category: "Container Management"},
		{ID: CurateImages, Name: "Curate Docker images", Shortcut: "", Aliases: []string{"images", "unused images", "prune images", "cleanup images"}, Enabled: true, Category: "Docker Resources"},
		{ID: CurateNetworks, Name: "Curate Docker networks", Shortcut: "", Aliases: []string{"networks", "unused networks", "prune networks", "address pool", "cleanup networks"}, Enabled: true, Category: "Docker Resources"},
		{ID: CurateVolumes, Name: "Curate Docker volumes", Shortcut: "", Aliases: []string{"volumes", "unused volumes", "prune volumes", "cleanup volumes"}, Enabled: true, Category: "Docker Resources"},
		{ID: CurateCompose, Name: "Curate Compose files", Shortcut: "", Aliases: []string{"compose files", "compose catalog", "stacks", "unused compose", "deploy compose"}, Enabled: true, Category: "Docker Resources"},
		{ID: ExecShell, Name: "Open a shell inside the selected container", Shortcut: "e", Aliases: []string{"exec", "shell", "terminal", "ssh into container"}, Enabled: canExec, Category: "Container Info"},
		{ID: FocusLogs, Name: "Show logs", Shortcut: "l", Aliases: []string{"tail"}, Enabled: hasContainer, Category: "Container Info"},
		{ID: ShowProblems, Name: "Show problems", Shortcut: "p", Aliases: []string{"issues", "health", "unhealthy", "restarting"}, Enabled: true, Category: "Container Info"},
		{ID: ShowStats, Name: "Show stats", Shortcut: "g", Aliases: []string{"graphs", "sparklines", "metrics", "cpu", "memory"}, Enabled: true, Category: "Container Info"},
		{ID: OpenCopy, Name: "Copy selected detail", Shortcut: "c", Aliases: []string{"clipboard", "copy id", "copy port", "copy label", "copy mount"}, Enabled: hasContainer, Category: "Container Info"},
		{ID: OpenPort, Name: "Open selected port", Shortcut: "o", Aliases: []string{"browser", "url", "localhost", "published port"}, Enabled: hasOpenPort(selected), Category: "Container Info"},
		{ID: OpenMount, Name: "Open selected mount", Shortcut: "o", Aliases: []string{"folder", "path", "volume", "bind mount"}, Enabled: hasOpenMount(selected), Category: "Container Info"},
		{ID: OpenFilter, Name: "Filter projects and containers", Shortcut: "/", Aliases: []string{"search"}, Enabled: true, Category: "Navigation"},
		{ID: OpenLogFilter, Name: "Filter visible logs", Shortcut: "/", Aliases: []string{"logs search", "log search"}, Enabled: hasContainer, Category: "Navigation"},
		{ID: OpenTheme, Name: "Choose theme", Shortcut: "T", Aliases: []string{"themes", "palette", "colors"}, Enabled: true, Category: "Settings"},
		{ID: OpenSettings, Name: "Open settings", Shortcut: ",", Aliases: []string{"preferences", "options", "config"}, Enabled: true, Category: "Settings"},
		{ID: OpenSystems, Name: "Manage systems", Shortcut: "S", Aliases: []string{"hosts", "profiles", "docker hosts", "remote"}, Enabled: true, Category: "Settings"},
		{ID: OpenHelp, Name: "Show keyboard help", Shortcut: "?", Aliases: []string{"keys"}, Enabled: true, Category: "Utility"},
		{ID: OpenAbout, Name: "Show about screen", Shortcut: "A", Aliases: []string{"about", "splash", "credits"}, Enabled: true, Category: "Utility"},
		{ID: ShutdownHost, Name: "Shut down host machine", Shortcut: "", Aliases: []string{"power off", "poweroff", "halt", "shutdown server"}, Enabled: true, Category: "System"},
		{ID: RebootHost, Name: "Reboot host machine", Shortcut: "", Aliases: []string{"restart host", "restart server", "reboot server"}, Enabled: true, Category: "System"},
		{ID: CommandPalette, Name: "Command palette", Shortcut: "ctrl+k", Aliases: []string{"commands"}, Enabled: true, Category: "Utility"},
		{ID: Quit, Name: "Quit WhatTheDock", Shortcut: "q", Aliases: []string{"exit"}, Enabled: true, Category: "Utility"},
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
