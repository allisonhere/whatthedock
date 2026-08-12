package docker

import (
	"testing"
	"time"

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

func TestFromStatsAggregatesDockerStats(t *testing.T) {
	read := time.Unix(100, 0)
	stats := FromStats("local", "abcdef", container.StatsResponse{
		Read: read,
		CPUStats: container.CPUStats{
			CPUUsage:    container.CPUUsage{TotalUsage: 300, PercpuUsage: []uint64{1, 2}},
			SystemUsage: 1100,
			OnlineCPUs:  2,
		},
		PreCPUStats: container.CPUStats{
			CPUUsage:    container.CPUUsage{TotalUsage: 100},
			SystemUsage: 100,
		},
		MemoryStats: container.MemoryStats{
			Usage: 512,
			Limit: 1024,
			Stats: map[string]uint64{"inactive_file": 128},
		},
		Networks: map[string]container.NetworkStats{
			"bridge": {RxBytes: 10, TxBytes: 20},
			"app":    {RxBytes: 30, TxBytes: 40},
		},
		BlkioStats: container.BlkioStats{IoServiceBytesRecursive: []container.BlkioStatEntry{
			{Op: "Read", Value: 50},
			{Op: "Write", Value: 70},
		}},
		PidsStats: container.PidsStats{Current: 9},
	})

	if stats.ID.Host != "local" || stats.ID.ID != "abcdef" {
		t.Fatalf("id = %#v", stats.ID)
	}
	if stats.Read != read {
		t.Fatalf("read = %v, want %v", stats.Read, read)
	}
	if stats.CPUPercent != 40 {
		t.Fatalf("CPUPercent = %v, want 40", stats.CPUPercent)
	}
	if stats.MemoryUsage != 384 || stats.MemoryLimit != 1024 {
		t.Fatalf("memory = %d/%d, want 384/1024", stats.MemoryUsage, stats.MemoryLimit)
	}
	if stats.NetworkRx != 40 || stats.NetworkTx != 60 {
		t.Fatalf("network = %d/%d, want 40/60", stats.NetworkRx, stats.NetworkTx)
	}
	if stats.BlockRead != 50 || stats.BlockWrite != 70 {
		t.Fatalf("block = %d/%d, want 50/70", stats.BlockRead, stats.BlockWrite)
	}
	if stats.PIDs != 9 {
		t.Fatalf("PIDs = %d, want 9", stats.PIDs)
	}
}
