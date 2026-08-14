package systems

import (
	"context"
	"reflect"
	"testing"

	"github.com/allisonhere/whatthedock/internal/config"
)

func TestFactoryBuildsSSHTunnelCommand(t *testing.T) {
	var gotName string
	var gotArgs []string
	factory := Factory{Runner: func(_ context.Context, name string, args ...string) error {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil
	}}

	provider, err := factory.Provider(context.Background(), config.System{
		ID:           "jarvis",
		Name:         "Jarvis",
		Kind:         "ssh",
		SSHHost:      "allie@jarvis",
		RemoteSocket: "/var/run/docker.sock",
		LocalSocket:  "/tmp/whatthedock-test-jarvis.sock",
	})
	if err != nil {
		t.Fatalf("Provider() err = %v", err)
	}
	if provider == nil {
		t.Fatal("Provider() returned nil provider")
	}
	_ = provider.Close()

	wantArgs := []string{"-fN", "-L", "/tmp/whatthedock-test-jarvis.sock:/var/run/docker.sock", "allie@jarvis"}
	if gotName != "ssh" || !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("runner = %q %#v, want ssh %#v", gotName, gotArgs, wantArgs)
	}
}

func TestFactoryBuildsSSHTunnelCommandWithSeparateUserAndPort(t *testing.T) {
	var gotName string
	var gotArgs []string
	factory := Factory{Runner: func(_ context.Context, name string, args ...string) error {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil
	}}

	provider, err := factory.Provider(context.Background(), config.System{
		ID:           "jarvis",
		Name:         "Jarvis",
		Kind:         "ssh",
		SSHHost:      "jarvis.lan",
		SSHUser:      "allie",
		SSHPort:      "2222",
		RemoteSocket: "/var/run/docker.sock",
		LocalSocket:  "/tmp/whatthedock-test-jarvis.sock",
	})
	if err != nil {
		t.Fatalf("Provider() err = %v", err)
	}
	if provider == nil {
		t.Fatal("Provider() returned nil provider")
	}
	_ = provider.Close()

	wantArgs := []string{"-fN", "-p", "2222", "-L", "/tmp/whatthedock-test-jarvis.sock:/var/run/docker.sock", "allie@jarvis.lan"}
	if gotName != "ssh" || !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("runner = %q %#v, want ssh %#v", gotName, gotArgs, wantArgs)
	}
}

func TestSSHTargetOmitsEmptyUser(t *testing.T) {
	if got := SSHTarget(config.System{SSHHost: "jarvis"}); got != "jarvis" {
		t.Fatalf("SSHTarget() = %q, want jarvis", got)
	}
}

func TestDockerHostFor(t *testing.T) {
	tests := []struct {
		name   string
		system config.System
		want   string
	}{
		{name: "local default", system: config.System{Kind: "local"}, want: ""},
		{name: "local host", system: config.System{Kind: "local", DockerHost: "tcp://host:2375"}, want: "tcp://host:2375"},
		{name: "ssh", system: config.System{Kind: "ssh", LocalSocket: "/tmp/x.sock"}, want: "unix:///tmp/x.sock"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DockerHostFor(tt.system); got != tt.want {
				t.Fatalf("DockerHostFor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDockerHostLabel(t *testing.T) {
	tests := []struct {
		name   string
		system config.System
		want   string
	}{
		{name: "local default", system: config.System{Kind: "local"}, want: "docker default"},
		{name: "local host", system: config.System{Kind: "local", DockerHost: "tcp://host:2375"}, want: "tcp://host:2375"},
		{name: "ssh", system: config.System{Kind: "ssh", LocalSocket: "/tmp/x.sock"}, want: "unix:///tmp/x.sock"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DockerHostLabel(tt.system); got != tt.want {
				t.Fatalf("DockerHostLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}
