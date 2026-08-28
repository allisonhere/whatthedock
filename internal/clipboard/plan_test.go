package clipboard

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/domain"
)

// noopTargetProvider is a minimal app.Provider stub — Plan only ever calls
// Host/Snapshot/Images/Networks/Volumes, so every other method panics if
// exercised, the same convention internal/ui/ai_test.go's noopProvider
// uses. Kept local to this package (rather than reusing internal/ui's
// fakeProvider) since internal/clipboard must not import internal/ui.
type noopTargetProvider struct{ host domain.Host }

func (p noopTargetProvider) Host() domain.Host        { return p.host }
func (noopTargetProvider) Ping(context.Context) error { return nil }
func (noopTargetProvider) Snapshot(context.Context) (domain.Snapshot, error) {
	return domain.Snapshot{}, nil
}
func (noopTargetProvider) Events(context.Context) (<-chan domain.ContainerEvent, error) {
	panic("not implemented")
}
func (noopTargetProvider) Container(context.Context, domain.ResourceID) (domain.Container, error) {
	panic("not implemented")
}
func (noopTargetProvider) ContainerStats(context.Context, domain.ResourceID) (domain.ContainerStats, error) {
	panic("not implemented")
}
func (noopTargetProvider) Logs(context.Context, domain.ResourceID, app.LogOptions) (io.ReadCloser, error) {
	panic("not implemented")
}
func (noopTargetProvider) CreateContainer(context.Context, app.ContainerCreateSpec) (domain.ResourceID, error) {
	panic("not implemented")
}
func (noopTargetProvider) RenameContainer(context.Context, domain.ResourceID, string) error {
	panic("not implemented")
}
func (noopTargetProvider) StartContainer(context.Context, domain.ResourceID) error {
	panic("not implemented")
}
func (noopTargetProvider) StopContainer(context.Context, domain.ResourceID) error {
	panic("not implemented")
}
func (noopTargetProvider) RestartContainer(context.Context, domain.ResourceID) error {
	panic("not implemented")
}
func (noopTargetProvider) RemoveContainer(context.Context, domain.ResourceID, bool) error {
	panic("not implemented")
}
func (noopTargetProvider) PullImage(context.Context, string, func(app.PullProgress)) error {
	panic("not implemented")
}
func (noopTargetProvider) Images(context.Context) ([]domain.Image, error) { return nil, nil }
func (noopTargetProvider) RemoveImage(context.Context, string) error {
	panic("not implemented")
}
func (noopTargetProvider) Networks(context.Context) ([]domain.Network, error) { return nil, nil }
func (noopTargetProvider) CreateNetwork(context.Context, string) error {
	panic("not implemented")
}
func (noopTargetProvider) RemoveNetwork(context.Context, string) error {
	panic("not implemented")
}
func (noopTargetProvider) Volumes(context.Context) ([]domain.Volume, error) { return nil, nil }
func (noopTargetProvider) RemoveVolume(context.Context, string) error {
	panic("not implemented")
}
func (noopTargetProvider) Close() error { return nil }

// fakeTargetProvider layers real Snapshot/Images/Networks/Volumes data on
// top of noopTargetProvider for the actual conflict-detection tests.
type fakeTargetProvider struct {
	noopTargetProvider
	snapshot domain.Snapshot
	images   []domain.Image
	networks []domain.Network
	volumes  []domain.Volume
}

func (f fakeTargetProvider) Snapshot(context.Context) (domain.Snapshot, error) {
	return f.snapshot, nil
}
func (f fakeTargetProvider) Images(context.Context) ([]domain.Image, error) { return f.images, nil }
func (f fakeTargetProvider) Networks(context.Context) ([]domain.Network, error) {
	return f.networks, nil
}
func (f fakeTargetProvider) Volumes(context.Context) ([]domain.Volume, error) { return f.volumes, nil }

func testPortable() PortableContainer {
	return PortableContainer{
		Name:          "radarr",
		Image:         "lscr.io/linuxserver/radarr:latest",
		RestartPolicy: "unless-stopped",
		Env: []PortableEnv{
			{Key: "PUID", Value: "1000"},
			{Key: "API_KEY", Value: "topsecret", Secret: true},
		},
		Ports: []PortablePort{
			{HostPort: 7878, ContainerPort: 7878, Protocol: "tcp", Published: true},
		},
		Mounts: []PortableMount{
			{Type: "bind", Source: "/srv/media/radarr", Target: "/config"},
			{Type: "volume", Source: "radarr-data", Target: "/data"},
		},
		Networks: []PortableNetwork{{Name: "media_default", Aliases: []string{"radarr"}}},
	}
}

func TestPlanDetectsNameCollision(t *testing.T) {
	target := fakeTargetProvider{
		snapshot: domain.Snapshot{Standalone: []domain.Container{{Name: "radarr"}}},
	}
	plan, err := Plan(context.Background(), target, testPortable())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !plan.Blocked() {
		t.Fatal("Blocked() = false, want true for a name collision")
	}
	if !hasConflict(plan.Conflicts, "name", SeverityBlock) {
		t.Fatalf("conflicts = %#v, want a blocking name conflict", plan.Conflicts)
	}
}

func TestPlanNoCollisionWhenNameFree(t *testing.T) {
	target := fakeTargetProvider{}
	plan, err := Plan(context.Background(), target, testPortable())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Blocked() {
		t.Fatalf("Blocked() = true, want false: %#v", plan.Conflicts)
	}
	if !hasConflict(plan.Conflicts, "name", SeverityOK) {
		t.Fatalf("conflicts = %#v, want an ok name conflict", plan.Conflicts)
	}
}

func TestPlanDetectsPortConflict(t *testing.T) {
	target := fakeTargetProvider{
		snapshot: domain.Snapshot{Standalone: []domain.Container{
			{Name: "other", Ports: []domain.Port{{Private: 80, Public: 7878, Type: "tcp"}}},
		}},
	}
	plan, err := Plan(context.Background(), target, testPortable())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !plan.Blocked() {
		t.Fatal("Blocked() = false, want true for a port collision")
	}
	if !hasConflict(plan.Conflicts, "port", SeverityBlock) {
		t.Fatalf("conflicts = %#v, want a blocking port conflict", plan.Conflicts)
	}
}

func TestPlanDetectsMissingImageAndSetsNeedsPull(t *testing.T) {
	target := fakeTargetProvider{}
	plan, err := Plan(context.Background(), target, testPortable())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !plan.NeedsPull {
		t.Fatal("NeedsPull = false, want true when the image isn't present")
	}
	if !hasConflict(plan.Conflicts, "image", SeverityWarn) {
		t.Fatalf("conflicts = %#v, want a warn image conflict", plan.Conflicts)
	}
}

func TestPlanImageAvailableSkipsPull(t *testing.T) {
	target := fakeTargetProvider{
		images: []domain.Image{{RepoTags: []string{"lscr.io/linuxserver/radarr:latest"}}},
	}
	plan, err := Plan(context.Background(), target, testPortable())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.NeedsPull {
		t.Fatal("NeedsPull = true, want false: image is already present")
	}
	if !hasConflict(plan.Conflicts, "image", SeverityOK) {
		t.Fatalf("conflicts = %#v, want an ok image conflict", plan.Conflicts)
	}
}

func TestPlanDetectsMissingNetworkAndOffersCreate(t *testing.T) {
	target := fakeTargetProvider{}
	plan, err := Plan(context.Background(), target, testPortable())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.NetworksToCreate) != 1 || plan.NetworksToCreate[0] != "media_default" {
		t.Fatalf("NetworksToCreate = %#v, want [media_default]", plan.NetworksToCreate)
	}
	if !hasConflict(plan.Conflicts, "network", SeverityWarn) {
		t.Fatalf("conflicts = %#v, want a warn network conflict", plan.Conflicts)
	}
}

func TestPlanNetworkAvailableSkipsCreate(t *testing.T) {
	target := fakeTargetProvider{networks: []domain.Network{{Name: "media_default"}}}
	plan, err := Plan(context.Background(), target, testPortable())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(plan.NetworksToCreate) != 0 {
		t.Fatalf("NetworksToCreate = %#v, want none", plan.NetworksToCreate)
	}
}

func TestPlanMissingNamedVolumeIsInformationalNotBlocking(t *testing.T) {
	target := fakeTargetProvider{}
	plan, err := Plan(context.Background(), target, testPortable())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Blocked() {
		t.Fatalf("Blocked() = true, want false: a missing named volume is auto-created, not fatal: %#v", plan.Conflicts)
	}
	if !hasConflict(plan.Conflicts, "volume", SeverityOK) {
		t.Fatalf("conflicts = %#v, want an informational volume conflict", plan.Conflicts)
	}
}

func TestPlanFlagsSecretEnvCount(t *testing.T) {
	target := fakeTargetProvider{}
	plan, err := Plan(context.Background(), target, testPortable())
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	found := false
	for _, c := range plan.Conflicts {
		if c.Kind == "secret-env" {
			found = true
			if c.Message == "" {
				t.Fatal("secret-env conflict message is empty")
			}
			for _, secret := range []string{"topsecret"} {
				if strings.Contains(c.Message, secret) || strings.Contains(c.Detail, secret) {
					t.Fatalf("conflict %#v leaks the secret value", c)
				}
			}
		}
	}
	if !found {
		t.Fatal("no secret-env conflict found, want one for the API_KEY var")
	}
}

func TestPlanFlagsPrivilegedAndCapabilities(t *testing.T) {
	pc := testPortable()
	pc.Privileged = true
	pc.CapAdd = []string{"NET_ADMIN"}
	target := fakeTargetProvider{}
	plan, err := Plan(context.Background(), target, pc)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !hasConflict(plan.Conflicts, "privileged", SeverityWarn) {
		t.Fatalf("conflicts = %#v, want a privileged warning", plan.Conflicts)
	}
}

func TestBindPathConflictReportsMissingPath(t *testing.T) {
	m := PortableMount{Type: "bind", Source: "/data/media/calibre", Target: "/config"}
	conflict := BindPathConflict(m, func(string) bool { return false })
	if conflict == nil {
		t.Fatal("BindPathConflict() = nil, want a conflict for a missing path")
	}
	// Block, not Warn: Docker's daemon refuses a missing bind source
	// outright (it doesn't auto-create it, unlike a missing named volume),
	// so this is a guaranteed create-time failure, not a "maybe fine"
	// warning — confirmed live when this was still Warn (see
	// BindPathConflict's own doc comment).
	if conflict.Severity != SeverityBlock || conflict.Detail != m.Source {
		t.Fatalf("conflict = %#v, want a blocking conflict naming %q", conflict, m.Source)
	}
}

func TestBindPathConflictNilWhenPathExists(t *testing.T) {
	m := PortableMount{Type: "bind", Source: "/srv/media", Target: "/config"}
	if conflict := BindPathConflict(m, func(string) bool { return true }); conflict != nil {
		t.Fatalf("BindPathConflict() = %#v, want nil when the path exists", conflict)
	}
}

func TestBindPathConflictNilForNamedVolume(t *testing.T) {
	m := PortableMount{Type: "volume", Source: "radarr-data", Target: "/data"}
	if conflict := BindPathConflict(m, func(string) bool { return false }); conflict != nil {
		t.Fatalf("BindPathConflict() = %#v, want nil for a named volume", conflict)
	}
}

func hasConflict(conflicts []PasteConflict, kind string, severity ConflictSeverity) bool {
	for _, c := range conflicts {
		if c.Kind == kind && c.Severity == severity {
			return true
		}
	}
	return false
}
