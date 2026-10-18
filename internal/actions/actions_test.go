package actions

import (
	"context"
	"testing"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/domain"
)

type recordingProvider struct {
	app.Provider
	pauses []bool
	kills  int
}

func (r *recordingProvider) SetContainerPaused(_ context.Context, _ domain.ResourceID, paused bool) error {
	r.pauses = append(r.pauses, paused)
	return nil
}

func (r *recordingProvider) KillContainer(context.Context, domain.ResourceID) error {
	r.kills++
	return nil
}

func command(t *testing.T, selected *domain.Container, id ID) Command {
	t.Helper()
	for _, c := range Catalog(selected) {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("command %q not in catalog", id)
	return Command{}
}

func TestPauseTogglesAndIsGatedByState(t *testing.T) {
	provider := &recordingProvider{}
	running := &domain.Container{State: domain.StateRunning}
	paused := &domain.Container{State: domain.StatePaused}
	stopped := &domain.Container{State: domain.StateStopped}

	if err := command(t, running, Pause).Run(context.Background(), provider, running); err != nil {
		t.Fatal(err)
	}
	if err := command(t, paused, Pause).Run(context.Background(), provider, paused); err != nil {
		t.Fatal(err)
	}
	if len(provider.pauses) != 2 || !provider.pauses[0] || provider.pauses[1] {
		t.Fatalf("pauses = %v, want [true false]", provider.pauses)
	}
	if command(t, stopped, Pause).Enabled || command(t, nil, Pause).Enabled {
		t.Fatal("pause enabled for a stopped or missing container")
	}
}

func TestKillEnabledOnlyForLiveContainers(t *testing.T) {
	provider := &recordingProvider{}
	running := &domain.Container{State: domain.StateRunning}
	if err := command(t, running, Kill).Run(context.Background(), provider, running); err != nil || provider.kills != 1 {
		t.Fatalf("kills = %d, err = %v, want 1 kill", provider.kills, err)
	}
	for _, state := range []domain.ContainerState{domain.StateStopped, domain.StateExited} {
		if command(t, &domain.Container{State: state}, Kill).Enabled {
			t.Fatalf("kill enabled for %s container", state)
		}
	}
}
