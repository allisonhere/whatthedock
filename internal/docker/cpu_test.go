package docker

import (
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

func oneShotStats(total, system uint64) container.StatsResponse {
	return container.StatsResponse{CPUStats: container.CPUStats{
		CPUUsage:    container.CPUUsage{TotalUsage: total},
		SystemUsage: system,
		OnlineCPUs:  2,
	}}
}

// A one-shot response has no precpu_stats, so CPU% must come from the
// previous poll of the same container — not the container's lifetime usage
// divided by the host's uptime.
func TestPrimeCPUUsesPreviousPollAsBaseline(t *testing.T) {
	p := &LocalProvider{}
	start := time.Unix(1000, 0)

	first := oneShotStats(5_000, 100_000)
	p.primeCPU("c1", &first, start)
	if got := cpuPercent(first); got != 0 {
		t.Fatalf("first poll CPUPercent = %v, want 0 (no baseline yet, not a lifetime average)", got)
	}

	second := oneShotStats(5_300, 100_300)
	p.primeCPU("c1", &second, start.Add(2*time.Second))
	// cpuDelta 300 / systemDelta 300 * 2 cores * 100
	if got := cpuPercent(second); got != 200 {
		t.Fatalf("second poll CPUPercent = %v, want 200", got)
	}
}

// Pollers that hit the same container back to back must not replace the
// baseline with a near-zero-width window.
func TestPrimeCPUKeepsBaselineForRapidRepolls(t *testing.T) {
	p := &LocalProvider{}
	start := time.Unix(1000, 0)

	base := oneShotStats(1_000, 10_000)
	p.primeCPU("c1", &base, start)

	rapid := oneShotStats(1_010, 10_020)
	p.primeCPU("c1", &rapid, start.Add(10*time.Millisecond))

	later := oneShotStats(1_200, 10_400)
	p.primeCPU("c1", &later, start.Add(2*time.Second))
	// Baseline is still the first poll: 200/400*2*100.
	if got := cpuPercent(later); got != 100 {
		t.Fatalf("CPUPercent = %v, want 100 measured against the original baseline", got)
	}
}

func TestPrimeCPUPreservesDaemonSuppliedBaseline(t *testing.T) {
	p := &LocalProvider{}
	stats := oneShotStats(300, 1100)
	stats.PreCPUStats = container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 100}, SystemUsage: 100}
	p.primeCPU("c1", &stats, time.Unix(1000, 0))
	if stats.PreCPUStats.CPUUsage.TotalUsage != 100 || stats.PreCPUStats.SystemUsage != 100 {
		t.Fatalf("PreCPUStats overwritten: %#v", stats.PreCPUStats)
	}
	if len(p.cpuPrev) != 0 {
		t.Fatalf("cpuPrev = %v, want untouched when the daemon supplied precpu_stats", p.cpuPrev)
	}
}

func TestPrimeCPUDropsStaleBaselines(t *testing.T) {
	p := &LocalProvider{}
	start := time.Unix(1000, 0)
	old := oneShotStats(1_000, 10_000)
	p.primeCPU("gone", &old, start)

	later := oneShotStats(2_000, 20_000)
	p.primeCPU("other", &later, start.Add(2*cpuSampleTTL))
	if _, ok := p.cpuPrev["gone"]; ok {
		t.Fatal("baseline for a container nobody polled within cpuSampleTTL was kept")
	}
}
