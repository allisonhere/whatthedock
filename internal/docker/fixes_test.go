package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
)

// cgroup v1 reports both a per-cgroup inactive_file and total_inactive_file;
// docker stats subtracts the total.
func TestMemoryUsagePrefersTotalInactiveFile(t *testing.T) {
	got := memoryUsage(container.MemoryStats{Usage: 1000, Stats: map[string]uint64{"inactive_file": 100, "total_inactive_file": 300}})
	if got != 700 {
		t.Fatalf("memoryUsage = %d, want 700 (usage - total_inactive_file)", got)
	}
}

func TestMemoryUsageCgroupV2AndNoCacheKey(t *testing.T) {
	if got := memoryUsage(container.MemoryStats{Usage: 1000, Stats: map[string]uint64{"inactive_file": 100}}); got != 900 {
		t.Fatalf("cgroup v2 memoryUsage = %d, want 900", got)
	}
	// The pre-19.03 "cache" key is not what docker stats uses any more.
	if got := memoryUsage(container.MemoryStats{Usage: 1000, Stats: map[string]uint64{"cache": 400}}); got != 1000 {
		t.Fatalf("memoryUsage with only cache = %d, want 1000", got)
	}
}

func TestFromInspectExpandsRangedPortBindings(t *testing.T) {
	ctr := FromInspect("h", container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{HostConfig: &container.HostConfig{
			PortBindings: nat.PortMap{
				"8000-8002/tcp": {{HostPort: "9000-9002"}},
				"53/udp":        {{HostIP: "127.0.0.1", HostPort: "5353"}},
			},
		}},
	})
	type pair struct {
		private, public uint16
		proto           string
	}
	var got []pair
	for _, p := range ctr.ConfiguredPorts {
		got = append(got, pair{p.Private, p.Public, p.Type})
	}
	want := []pair{{53, 5353, "udp"}, {8000, 9000, "tcp"}, {8001, 9001, "tcp"}, {8002, 9002, "tcp"}}
	if len(got) != len(want) {
		t.Fatalf("ConfiguredPorts = %v, want %v (no container port 0)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ConfiguredPorts = %v, want %v", got, want)
		}
	}
}
