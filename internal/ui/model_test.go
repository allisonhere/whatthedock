package ui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/tidedock/internal/app"
	"github.com/allisonhere/tidedock/internal/domain"
)

type fakeProvider struct {
	host       domain.Host
	snapshot   domain.Snapshot
	containers map[string]domain.Container
	starts     int
	stops      int
	restarts   int
}

func (f *fakeProvider) Host() domain.Host          { return f.host }
func (f *fakeProvider) Ping(context.Context) error { return nil }
func (f *fakeProvider) Snapshot(context.Context) (domain.Snapshot, error) {
	return f.snapshot, nil
}
func (f *fakeProvider) Container(_ context.Context, id domain.ResourceID) (domain.Container, error) {
	return f.containers[id.ID], nil
}
func (f *fakeProvider) Logs(context.Context, domain.ResourceID, app.LogOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("one\ntwo\n")), nil
}
func (f *fakeProvider) StartContainer(context.Context, domain.ResourceID) error {
	f.starts++
	return nil
}
func (f *fakeProvider) StopContainer(context.Context, domain.ResourceID) error {
	f.stops++
	return nil
}
func (f *fakeProvider) RestartContainer(context.Context, domain.ResourceID) error {
	f.restarts++
	return nil
}
func (f *fakeProvider) Close() error { return nil }

func TestBuildRowsCollapseAndFilter(t *testing.T) {
	model := testModel()
	model.rows = model.buildRows()
	if len(model.rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(model.rows))
	}
	model.collapsed["media"] = true
	model.rows = model.buildRows()
	if len(model.rows) != 1 {
		t.Fatalf("collapsed rows = %d, want project row only", len(model.rows))
	}
	model.collapsed["media"] = false
	model.filter = "rad"
	model.rows = model.buildRows()
	if got := rowLabels(model.rows); strings.Join(got, ",") != "media,radarr,radarr-1" {
		t.Fatalf("filtered rows = %#v", got)
	}
}

func TestSelectionMovementClamps(t *testing.T) {
	model := testModel()
	model.rows = model.buildRows()
	model.moveCursor(100)
	if model.cursor != len(model.rows)-1 {
		t.Fatalf("cursor = %d, want last row", model.cursor)
	}
	model.moveCursor(-100)
	if model.cursor != 0 {
		t.Fatalf("cursor = %d, want first row", model.cursor)
	}
}

func TestActionStartStopUsesSelectedState(t *testing.T) {
	provider := newFakeProvider()
	model := NewModel(provider)
	model.snapshot = provider.snapshot
	model.rows = model.buildRows()
	for i, row := range model.rows {
		if row.container != nil && row.container.ID.ID == "1" {
			model.cursor = i
			break
		}
	}
	msg := runCmd(t, model.actionCmd("start-stop-container", "start/stop")).(actionDoneMsg)
	if msg.err != nil {
		t.Fatalf("action err = %v", msg.err)
	}
	if provider.stops != 1 {
		t.Fatalf("stops = %d, want 1", provider.stops)
	}
}

func TestResizeViewDoesNotPanic(t *testing.T) {
	model := testModel()
	model.width, model.height = 20, 6
	_ = model.View()
	model.width, model.height = 120, 40
	_ = model.View()
}

func TestSnapshotErrorShowsDockerUnavailableState(t *testing.T) {
	model := NewModel(newFakeProvider())
	updated, _ := model.Update(snapshotMsg{err: errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock")})
	got := updated.(Model)
	if !got.statusErr {
		t.Fatal("statusErr = false, want true")
	}
	if !strings.Contains(got.status, "Docker is unavailable") {
		t.Fatalf("status = %q, want actionable Docker unavailable message", got.status)
	}
	if got.selected != nil {
		t.Fatalf("selected = %#v, want nil after unavailable snapshot", got.selected)
	}
}

func TestThemePickerPreviewsAndCancels(t *testing.T) {
	model := testModel()
	original := model.theme.Name
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'T'}})
	model = updated.(Model)
	if model.overlay != overlayThemePicker {
		t.Fatalf("overlay = %v, want theme picker", model.overlay)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = updated.(Model)
	if model.theme.Name == original {
		t.Fatalf("theme after preview = %q, want different from %q", model.theme.Name, original)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.overlay != overlayNone {
		t.Fatalf("overlay after cancel = %v, want none", model.overlay)
	}
	if model.theme.Name != original {
		t.Fatalf("theme after cancel = %q, want restored %q", model.theme.Name, original)
	}
}

func TestOverlaysRenderSoftPanelChrome(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	for _, tc := range []struct {
		name    string
		overlay overlayMode
		want    string
	}{
		{"help", overlayHelp, "tidedock · help"},
		{"filter", overlayFilter, "tidedock · filter"},
		{"command", overlayCommandPalette, "tidedock · command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model.overlay = tc.overlay
			view := ansi.Strip(model.View())
			if !strings.Contains(view, tc.want) {
				t.Fatalf("View() missing soft panel title %q:\n%s", tc.want, view)
			}
		})
	}
}

func testModel() Model {
	provider := newFakeProvider()
	model := NewModel(provider)
	model.snapshot = provider.snapshot
	return model
}

func newFakeProvider() *fakeProvider {
	host := domain.Host{ID: "local", Name: "local"}
	containers := []domain.Container{
		{ID: domain.ResourceID{Host: "local", ID: "1"}, Name: "radarr-1", Image: "radarr", State: domain.StateRunning, Health: domain.HealthHealthy, Compose: domain.ComposeRef{Project: "media", Service: "radarr"}},
		{ID: domain.ResourceID{Host: "local", ID: "2"}, Name: "jellyfin-1", Image: "jellyfin", State: domain.StateStopped, Compose: domain.ComposeRef{Project: "media", Service: "jellyfin"}},
	}
	snapshot := domain.BuildSnapshot(host, containers, time.Unix(1, 0))
	return &fakeProvider{
		host:       host,
		snapshot:   snapshot,
		containers: map[string]domain.Container{"1": containers[0], "2": containers[1]},
	}
}

func rowLabels(rows []treeRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.label)
	}
	return out
}

func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("cmd is nil")
	}
	return cmd()
}
