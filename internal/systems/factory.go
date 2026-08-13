package systems

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/config"
	dockerprovider "github.com/allisonhere/whatthedock/internal/docker"
)

type Runner func(context.Context, string, ...string) error

type Factory struct {
	Runner Runner
}

func NewFactory() Factory {
	return Factory{Runner: runCommand}
}

func (f Factory) Provider(ctx context.Context, system config.System) (app.Provider, error) {
	system = config.NormalizeSystems(config.Settings{ActiveSystem: system.ID, Systems: []config.System{system}}).Systems[0]
	switch system.Kind {
	case "ssh":
		if err := f.ensureSSHTunnel(ctx, system); err != nil {
			return nil, err
		}
		return dockerprovider.NewProvider(system.ID, system.Name, "unix://"+system.LocalSocket)
	case "local", "":
		return dockerprovider.NewProvider(system.ID, system.Name, system.DockerHost)
	default:
		return nil, fmt.Errorf("unknown system kind %q", system.Kind)
	}
}

func (f Factory) ensureSSHTunnel(ctx context.Context, system config.System) error {
	args, err := SSHCommandArgs(system)
	if err != nil || args == nil {
		return err
	}
	runner := f.Runner
	if runner != nil {
		return runner(ctx, "ssh", args...)
	}
	cmd, err := SSHCommandContext(ctx, system)
	if err != nil || cmd == nil {
		return err
	}
	return cmd.Run()
}

func SSHCommand(system config.System) (*exec.Cmd, error) {
	args, err := SSHCommandArgs(system)
	if err != nil || args == nil {
		return nil, err
	}
	return exec.Command("ssh", args...), nil
}

func SSHCommandContext(ctx context.Context, system config.System) (*exec.Cmd, error) {
	args, err := SSHCommandArgs(system)
	if err != nil || args == nil {
		return nil, err
	}
	return exec.CommandContext(ctx, "ssh", args...), nil
}

func SSHCommandArgs(system config.System) ([]string, error) {
	system = config.NormalizeSystems(config.Settings{ActiveSystem: system.ID, Systems: []config.System{system}}).Systems[0]
	if system.SSHHost == "" {
		return nil, fmt.Errorf("ssh host is required")
	}
	if system.LocalSocket == "" {
		return nil, fmt.Errorf("local socket is required")
	}
	if system.RemoteSocket == "" {
		return nil, fmt.Errorf("remote socket is required")
	}
	if isSocket(system.LocalSocket) {
		return nil, nil
	}
	_ = os.Remove(system.LocalSocket)
	if err := os.MkdirAll(filepath.Dir(system.LocalSocket), 0o755); err != nil {
		return nil, err
	}
	args := []string{"-fN"}
	if system.SSHPort != "" {
		args = append(args, "-p", system.SSHPort)
	}
	args = append(args, "-L", system.LocalSocket+":"+system.RemoteSocket, SSHTarget(system))
	return args, nil
}

func SSHTarget(system config.System) string {
	if system.SSHUser == "" {
		return system.SSHHost
	}
	return system.SSHUser + "@" + system.SSHHost
}

func isSocket(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSocket != 0
}

func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Run()
}

func DockerHostLabel(system config.System) string {
	switch system.Kind {
	case "ssh":
		return "unix://" + system.LocalSocket
	case "local":
		if system.DockerHost != "" {
			return system.DockerHost
		}
		return "docker default"
	default:
		if system.DockerHost != "" {
			if u, err := url.Parse(system.DockerHost); err == nil && u.Scheme != "" {
				return system.DockerHost
			}
		}
		return system.Kind
	}
}
