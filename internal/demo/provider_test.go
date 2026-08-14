package demo

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/domain"
)

func TestProviderSnapshotContainsDemoComposeAndStandalone(t *testing.T) {
	provider := NewProvider()
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() err = %v", err)
	}
	if len(snapshot.Projects) != 2 {
		t.Fatalf("projects = %d, want 2", len(snapshot.Projects))
	}
	if len(snapshot.Standalone) != 1 {
		t.Fatalf("standalone = %d, want 1", len(snapshot.Standalone))
	}
	if snapshot.Host.ID != "demo" {
		t.Fatalf("host id = %q, want demo", snapshot.Host.ID)
	}
}

func TestProviderActionsMutateContainerState(t *testing.T) {
	provider := NewProvider()
	id := domain.ResourceID{Host: "demo", ID: "demo-nginx"}
	if err := provider.StopContainer(context.Background(), id); err != nil {
		t.Fatalf("StopContainer() err = %v", err)
	}
	ctr, err := provider.Container(context.Background(), id)
	if err != nil {
		t.Fatalf("Container() err = %v", err)
	}
	if ctr.State != domain.StateStopped {
		t.Fatalf("state after stop = %q, want stopped", ctr.State)
	}
	if err := provider.StartContainer(context.Background(), id); err != nil {
		t.Fatalf("StartContainer() err = %v", err)
	}
	ctr, _ = provider.Container(context.Background(), id)
	if ctr.State != domain.StateRunning {
		t.Fatalf("state after start = %q, want running", ctr.State)
	}
}

func TestProviderRemoveContainerDeletesEntry(t *testing.T) {
	provider := NewProvider()
	id := domain.ResourceID{Host: "demo", ID: "demo-nginx"}
	if err := provider.RemoveContainer(context.Background(), id, true); err != nil {
		t.Fatalf("RemoveContainer() err = %v", err)
	}
	if _, err := provider.Container(context.Background(), id); err == nil {
		t.Fatal("Container() after remove = nil error, want it to be gone")
	}
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() err = %v", err)
	}
	for _, project := range snapshot.Projects {
		for _, service := range project.Services {
			for _, ctr := range service.Containers {
				if ctr.ID == id {
					t.Fatalf("snapshot still contains removed container %v", id)
				}
			}
		}
	}
	for _, ctr := range snapshot.Standalone {
		if ctr.ID == id {
			t.Fatalf("snapshot still contains removed container %v", id)
		}
	}
}

func TestProviderRemoveContainerUnknownID(t *testing.T) {
	provider := NewProvider()
	err := provider.RemoveContainer(context.Background(), domain.ResourceID{Host: "demo", ID: "does-not-exist"}, true)
	if err == nil {
		t.Fatal("RemoveContainer(unknown id) = nil, want an error")
	}
}

func TestProviderPullImageSynthesizesProgress(t *testing.T) {
	provider := NewProvider()
	var calls []app.PullProgress
	err := provider.PullImage(context.Background(), "anything:latest", func(p app.PullProgress) {
		calls = append(calls, p)
	})
	if err != nil {
		t.Fatalf("PullImage() err = %v, want nil (no real registry in demo mode)", err)
	}
	if len(calls) == 0 {
		t.Fatal("PullImage() called onProgress zero times, want at least one synthesized update")
	}
}

func TestProviderPullImageNilCallbackDoesNotPanic(t *testing.T) {
	provider := NewProvider()
	if err := provider.PullImage(context.Background(), "anything:latest", nil); err != nil {
		t.Fatalf("PullImage() with nil onProgress err = %v, want nil", err)
	}
}

func TestProviderLogsStreamDemoLines(t *testing.T) {
	provider := NewProvider()
	stream, err := provider.Logs(context.Background(), domain.ResourceID{Host: "demo", ID: "demo-grafana"}, app.LogOptions{})
	if err != nil {
		t.Fatalf("Logs() err = %v", err)
	}
	defer stream.Close()
	data, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("ReadAll() err = %v", err)
	}
	if !strings.Contains(string(data), "grafana") && !strings.Contains(string(data), "HTTP Server") {
		t.Fatalf("logs = %q, want grafana-like demo lines", string(data))
	}
}

func TestProviderStatsVaryAcrossSamples(t *testing.T) {
	provider := NewProvider()
	id := domain.ResourceID{Host: "demo", ID: "demo-qbit"}
	first, err := provider.ContainerStats(context.Background(), id)
	if err != nil {
		t.Fatalf("ContainerStats() first err = %v", err)
	}
	second, err := provider.ContainerStats(context.Background(), id)
	if err != nil {
		t.Fatalf("ContainerStats() second err = %v", err)
	}
	if first.ID != id || second.ID != id {
		t.Fatalf("stats IDs = %#v/%#v, want %v", first.ID, second.ID, id)
	}
	if first.CPUPercent == second.CPUPercent &&
		first.MemoryUsage == second.MemoryUsage &&
		first.NetworkRx == second.NetworkRx &&
		first.BlockWrite == second.BlockWrite {
		t.Fatalf("stats did not vary across samples: %#v", first)
	}
}
