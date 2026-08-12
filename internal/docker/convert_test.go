package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"

	"github.com/allisonhere/tidedock/internal/domain"
)

func TestFromSummaryMapsComposeLabelsAndHealth(t *testing.T) {
	ctr := FromSummary("local", container.Summary{
		ID:      "abcdef",
		Names:   []string{"/media-radarr-1"},
		Image:   "lscr.io/linuxserver/radarr:latest",
		ImageID: "sha256:123",
		State:   "running",
		Status:  "Up 2 minutes (healthy)",
		Labels: map[string]string{
			domain.LabelComposeProject:     "media",
			domain.LabelComposeService:     "radarr",
			domain.LabelComposeContainerNo: "1",
		},
	})

	if ctr.ID.Host != "local" || ctr.ID.ID != "abcdef" {
		t.Fatalf("id = %#v", ctr.ID)
	}
	if ctr.Name != "media-radarr-1" {
		t.Fatalf("name = %q", ctr.Name)
	}
	if ctr.Compose.Project != "media" || ctr.Compose.Service != "radarr" {
		t.Fatalf("compose = %#v", ctr.Compose)
	}
	if ctr.State != domain.StateRunning || ctr.Health != domain.HealthHealthy {
		t.Fatalf("state/health = %q/%q", ctr.State, ctr.Health)
	}
}
