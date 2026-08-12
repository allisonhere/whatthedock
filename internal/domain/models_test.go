package domain

import (
	"strings"
	"testing"
	"time"
)

func TestBuildSnapshotGroupsComposeLabels(t *testing.T) {
	host := Host{ID: "local", Name: "local"}
	containers := []Container{
		{
			ID: ResourceID{Host: "local", ID: "1"}, Name: "radarr", Image: "radarr:latest",
			Compose: ComposeRef{Project: "media", Service: "radarr"},
		},
		{
			ID: ResourceID{Host: "local", ID: "2"}, Name: "grafana", Image: "grafana:latest",
			Compose: ComposeRef{Project: "monitoring", Service: "grafana"},
		},
		{
			ID: ResourceID{Host: "local", ID: "3"}, Name: "jellyfin", Image: "jellyfin:latest",
			Compose: ComposeRef{Project: "media", Service: "jellyfin"},
		},
		{ID: ResourceID{Host: "local", ID: "4"}, Name: "loose", Image: "nginx:latest"},
	}

	snapshot := BuildSnapshot(host, containers, time.Unix(10, 0))

	if len(snapshot.Projects) != 2 {
		t.Fatalf("projects = %d, want 2", len(snapshot.Projects))
	}
	if snapshot.Projects[0].Name != "media" {
		t.Fatalf("first project = %q, want sorted media", snapshot.Projects[0].Name)
	}
	if got := snapshot.Projects[0].Services[0].Name; got != "jellyfin" {
		t.Fatalf("first media service = %q, want jellyfin", got)
	}
	if len(snapshot.Standalone) != 1 || snapshot.Standalone[0].Name != "loose" {
		t.Fatalf("standalone = %#v, want loose container", snapshot.Standalone)
	}
}

func TestContainerSearchTextIncludesComposeAndState(t *testing.T) {
	ctr := Container{
		Name: "/postgres", Image: "postgres:16", State: StateRunning, Health: HealthHealthy,
		Compose: ComposeRef{Project: "infra", Service: "db"},
	}

	text := ctr.SearchText()
	for _, want := range []string{"postgres", "infra", "db", "running", "healthy"} {
		if !strings.Contains(text, want) {
			t.Fatalf("SearchText() = %q, missing %q", text, want)
		}
	}
}
