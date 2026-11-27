package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/demo"
	"github.com/allisonhere/whatthedock/internal/domain"
)

func TestDeleteTargetStaysPinned(t *testing.T) {
	m := modelSelectingStandalone("alpha", "alpine")
	p := m.provider.(*fakeProvider)
	alpha := *m.selected
	beta := domain.Container{ID: domain.ResourceID{Host: alpha.ID.Host, ID: "other"}, Name: "beta", Image: "alpine"}
	m.width, m.height = 120, 26
	m.snapshot = domain.BuildSnapshot(p.Host(), []domain.Container{alpha, beta}, time.Now())
	m.rows = m.buildRows()
	m.focusTreeIndex(0)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	m = next.(Model)

	next, _ = m.Update(tea.MouseMsg{X: 3, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = next.(Model)
	if m.selectedContainer().ID != alpha.ID {
		t.Fatal("mouse press changed the tree selection through a modal")
	}

	// A refresh can move the tree selection independently of mouse input.
	m.rows = []treeRow{{kind: rowContainer, container: &beta}}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	done := runCmd(t, cmd).(actionDoneMsg)
	if done.err != nil || !slices.Equal(p.removed, []domain.ResourceID{alpha.ID}) {
		t.Fatalf("delete err = %v, removed = %v, want only %v", done.err, p.removed, alpha.ID)
	}
}

func TestIgnoresPreviousSystemSnapshotAndStream(t *testing.T) {
	m := testModel()
	old := m.refreshCmd()()
	target := newFakeProvider()
	target.host = domain.Host{ID: "new", Name: "new"}
	next, _ := m.Update(systemSwitchMsg{provider: target, system: config.System{ID: "new", Name: "new", Kind: "local"}})
	m = next.(Model)
	current := m.refreshCmd()()
	next, _ = m.Update(current)
	m = next.(Model)
	currentSnapshot := m.snapshot.Refreshed
	next, cmd := m.Update(old)
	m = next.(Model)
	if cmd != nil || m.snapshot.Refreshed != currentSnapshot {
		t.Fatal("stale refresh overwrote the active system snapshot")
	}
	canceled := false
	_, cmd = m.Update(eventsStartedMsg{cancel: func() { canceled = true }})
	if !canceled || cmd != nil {
		t.Fatal("stale event stream was not canceled")
	}
}

func TestStackLogStreamSurvivesRefreshAndPause(t *testing.T) {
	m := NewModel(demo.NewProvider())
	m.width, m.height = 100, 28
	snapshot, err := m.provider.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(snapshotMsg{snapshot: snapshot})
	m = next.(Model)
	for i, row := range m.rows {
		if row.kind == rowProject {
			m.focusTreeIndex(i)
			m.openStackLogs(row.project)
			break
		}
	}
	if m.stackLogs == "" {
		t.Fatal("demo has no stack to test")
	}
	m.mode = activityLogs
	for i := range 1000 {
		m.logLines = append(m.logLines, fmt.Sprintf("tagged log %04d", i))
	}
	queue := make(chan string, 1)
	m.logChan = queue
	m.pauseLogs()
	first := m.logLines[m.logStartIndex(len(m.logLines), m.logVisibleRows())]
	next, _ = m.Update(snapshotMsg{snapshot: snapshot})
	m = next.(Model)
	if m.logChan != queue || len(m.logLines) != 1000 {
		t.Fatal("routine refresh discarded the stack log stream")
	}
	queue <- "tagged log 1000"
	m.drainLogs()
	got := m.logLines[m.logStartIndex(len(m.logLines), m.logVisibleRows())]
	if m.logFollow || got != first {
		t.Fatalf("paused viewport changed from %q to %q", first, got)
	}
	if frame := ansi.Strip(m.View()); !strings.Contains(frame, first) {
		t.Fatalf("stack logs are missing from the activity pane: %q", first)
	}
}

func TestCompactLayoutShowsCommandsAndFocusedContent(t *testing.T) {
	m := NewModel(demo.NewProvider())
	snapshot, err := m.provider.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(snapshotMsg{snapshot: snapshot})
	m = next.(Model)
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		m.width, m.height = size[0], size[1]
		frame := ansi.Strip(m.View())
		if !strings.Contains(frame, "media-jellyfin-1") || !strings.Contains(frame, "? help") {
			t.Fatalf("%dx%d lost the focused tree contents or help hint", size[0], size[1])
		}
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if next.(Model).focus != paneActivity {
		t.Fatal("Tab did not switch the visible pane")
	}
}

func TestDemoDoesNotDispatchCompose(t *testing.T) {
	settings := config.Settings{ActiveSystem: "remote", Systems: []config.System{{ID: "remote", Kind: "ssh", SSHHost: "elsewhere"}}}
	m := NewModelWithSettings(demo.NewProvider(), settings, "")
	m.openCreateOverlayWithDraft(m.defaultCreateDraft())
	m.createDraft.Image = "alpine"
	m.createDraft.Mode = createModeCompose
	m.createDraft.Confirming = true
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd != nil || !strings.Contains(next.(Model).createNotice, "unavailable in demo mode") {
		t.Fatal("demo Compose confirmation was not blocked")
	}
}

func TestStandaloneEditLoadsInspectInsteadOfSummary(t *testing.T) {
	m := modelSelectingStandalone("app", "alpine")
	p := m.provider.(*fakeProvider)
	summary := *m.selected
	inspected := summary
	inspected.Env = []string{"TOKEN=a,b"}
	inspected.Mounts = []domain.Mount{{Type: "volume", Name: "data", Source: "/var/lib/docker/volumes/data/_data", Destination: "/data", ReadWrite: true}}
	inspected.NetworkMode = "host"
	inspected.RestartPolicy = "on-failure"
	inspected.RestartMaxRetries = 3
	inspected.ConfiguredPorts = []domain.Port{{Private: 80, Type: "tcp"}}
	p.containers[summary.ID.ID] = inspected
	m.rows = []treeRow{{kind: rowContainer, container: &summary}}
	m.selected = nil
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("edit did not request an inspect")
	}
	next, _ = m.Update(runCmd(t, cmd))
	m = next.(Model)
	spec, err := m.createDraft.ContainerSpec()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(spec.Env, inspected.Env) || len(spec.Mounts) != 1 || spec.Mounts[0].Source != "data" || spec.NetworkMode != "host" || spec.RestartPolicy != "on-failure" || spec.RestartMaxRetries != 3 || len(spec.Ports) != 1 || spec.Ports[0].HostPort != 0 {
		t.Fatalf("edit lost inspected configuration: %#v", spec)
	}
}

func TestComposeCLIUsesExplicitHostAndAllLayers(t *testing.T) {
	tmp := t.TempDir()
	stub := filepath.Join(tmp, "docker")
	capture := filepath.Join(tmp, "args")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s\\n' \"$DOCKER_CONTEXT\" \"$@\" > \"$WTD_TEST_ARGS\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp)
	t.Setenv("WTD_TEST_ARGS", capture)
	t.Setenv("DOCKER_HOST", "tcp://ambient.invalid:2375")
	t.Setenv("DOCKER_CONTEXT", "ambient-context")
	system := config.System{Kind: "local", DockerHost: "tcp://configured.invalid:2375"}
	spec := composeCreateSpec{System: system, Project: "app", BaseFile: "base.yml", AdditionalFiles: []string{"prod.yml", "other.yml"}}
	if err := runDockerCompose(t.Context(), spec, "up", "-d"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	want := "\n--host\ntcp://configured.invalid:2375\ncompose\n-p\napp\n-f\nbase.yml\n-f\nprod.yml\n-f\nother.yml\nup\n-d\n"
	if string(data) != want {
		t.Fatalf("command and context = %q, want %q", data, want)
	}
}

func TestLayeredComposeServiceEditsKeepInheritedFields(t *testing.T) {
	draft := createDraft{Mode: createModeCompose, Editing: true, ComposeFile: "/tmp/base.yml", composeLayers: []string{"/tmp/prod.yml"}, Project: "stack", Service: "app"}
	m := testModel()
	m.openCreateOverlayWithDraft(draft)
	if err := m.loadLayeredComposeFields(composeLayersMsg{service: "app", merged: "services:\n  app:\n    image: alpine\n    ports: ['9999:80']\n    environment: {KEEP: yes}\n"}); err != nil {
		t.Fatal(err)
	}
	m.createDraft.Ports = "7777:80"
	spec, err := m.createDraft.ComposeSpec(config.DefaultSystem())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(spec.Content, "ports: !override") || strings.Contains(spec.Content, "environment:") || strings.Contains(spec.Content, "image:") {
		t.Fatalf("generated override should contain only the changed ports: %s", spec.Content)
	}
	if !slices.Equal(spec.files(), []string{"/tmp/base.yml", "/tmp/prod.yml", "/tmp/compose.whatthedock.app.yml"}) {
		t.Fatalf("ordered Compose files = %v", spec.files())
	}
}

func TestLayeredComposeDeleteRemovesDefinitionsFromEveryFile(t *testing.T) {
	dir := t.TempDir()
	base, prod := filepath.Join(dir, "base.yml"), filepath.Join(dir, "prod.yml")
	for file, text := range map[string]string{
		base: "services:\n  app: {image: alpine}\n  sibling: {image: busybox}\n",
		prod: "services:\n  app: {restart: always}\n  sibling: {restart: always}\n",
	} {
		if err := os.WriteFile(file, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	origCompose, origOutput := composeCommand, dockerComposeOutput
	t.Cleanup(func() { composeCommand, dockerComposeOutput = origCompose, origOutput })
	composeCommand = func(_ context.Context, s composeCreateSpec, args ...string) error {
		if !slices.Equal(s.files(), []string{base, prod}) || !slices.Equal(args, []string{"rm", "-sf", "app"}) {
			t.Fatalf("unexpected Compose removal: %v %v", s.files(), args)
		}
		return nil
	}
	dockerComposeOutput = func(_ context.Context, _ config.System, _ string, files []string, args ...string) ([]byte, error) {
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil || baseComposeDefinesService(data, "app") {
				t.Fatalf("expected staged layer to exclude app: %s: %v", file, err)
			}
		}
		return []byte("sibling\n"), nil
	}
	spec := composeCreateSpec{Project: "stack", Service: "app", System: config.DefaultSystem(), BaseFile: base, AdditionalFiles: []string{prod}}
	if err := defaultApplyComposeDelete(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{base, prod} {
		data, err := os.ReadFile(file)
		if err != nil || baseComposeDefinesService(data, "app") || !baseComposeDefinesService(data, "sibling") {
			t.Fatalf("unexpected content after deletion: %s: %v, %s", file, err, data)
		}
	}
}

func TestHostPowerUsesTheConnectedSystemAfterStartupFallback(t *testing.T) {
	p := newFakeProvider()
	p.host = domain.Host{ID: "local", Name: "local"}
	settings := config.Settings{ActiveSystem: "remote", Systems: []config.System{{ID: "remote", Kind: "ssh", SSHHost: "elsewhere"}}}
	m := NewModelWithSettings(p, settings, "")
	previous := hostPowerRun
	t.Cleanup(func() { hostPowerRun = previous })
	called := false
	hostPowerRun = func(_ context.Context, system config.System, _ hostPowerKind, _ string) error {
		called = true
		if system.ID != "local" || system.Kind != "local" || system.SSHHost != "" {
			t.Fatalf("confirmed local reboot dispatched with %+v", system)
		}
		return nil
	}
	next, _ := m.openHostPowerConfirm(hostPowerReboot)
	m = next.(Model)
	m.hostPowerConfirmInput = []rune("local")
	_, cmd := m.handleHostPowerConfirmKey(tea.KeyMsg{Type: tea.KeyEnter})
	runCmd(t, cmd)
	if !called {
		t.Fatal("local reboot did not reach the stub")
	}
}

func TestCommandPaletteKeepsSelectedRowVisibleAndAcceptsSearchLetters(t *testing.T) {
	m := testModelWithSelectedContainer()
	m.width, m.height = 40, 12
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(Model)
	items := m.filteredCommands()
	name := items[len(items)-1].Name
	frame := ansi.Strip(m.View())
	if m.commandCursor != len(items)-1 || !strings.Contains(frame, name) || !strings.Contains(frame, "esc close") {
		t.Fatalf("final command %q or footer is missing from the visible palette:\n%s", name, frame)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = updated.(Model)
	if m.commandFilter != "k" {
		t.Fatal("typing k moved selection instead of starting a search")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ill界")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(Model)
	if len(m.filteredCommands()) != 1 || !strings.Contains(ansi.Strip(m.View()), "Kill selected") {
		t.Fatalf("palette failed to find Kill after editing Unicode query %q", m.commandFilter)
	}
}

func TestDashboardOpensContainerHiddenByFilterAndCollapsedStack(t *testing.T) {
	m := testModel()
	m.width, m.height = 100, 26
	m.collapsed["media"] = true
	m.filter = "nothing matches this filter"
	m.rows = m.buildRows()
	m.overlay = overlayDashboard
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil || m.overlay != overlayNone || m.focus != paneInspector || m.selectedContainer() == nil || m.filter != "" || m.collapsed["media"] {
		t.Fatalf("dashboard did not open the hidden container: overlay %v, focus %v, filter %q", m.overlay, m.focus, m.filter)
	}
}

func TestMouseTargetsThePaneUnderThePointer(t *testing.T) {
	m := testModelWithSelectedContainer()
	m.width, m.height = 120, 24
	m.focus = paneTree
	m.mode = activityLogs
	m.logLines = numberedLogLines(60)
	m.logFollow = true
	cursor := m.cursor
	next, _ := m.Update(tea.MouseMsg{
		X: m.leftPaneWidth() + 2, Y: 7, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp,
	})
	m = next.(Model)
	if m.focus != paneActivity || m.logFollow || m.cursor != cursor {
		t.Fatal("scrolling over logs did not move the log viewport")
	}
	m.focus = paneTree
	next, _ = m.Update(tea.MouseMsg{
		X: m.leftPaneWidth() + 2, Y: 7, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	if next.(Model).focus != paneActivity {
		t.Fatal("clicking the logs pane did not focus it")
	}
}

func TestTreeFilterRevealsMatchesAndCanBeClearedWithEscape(t *testing.T) {
	m := testModel()
	m.width, m.height = 80, 24
	m.focus = paneTree
	m.mode = activityLogs
	m.logFilter = "keep the log filter"
	m.collapsed["media"] = true
	m.applyTreeFilter("media")
	frame := ansi.Strip(m.View())
	if len(m.rows) != 3 || !strings.Contains(frame, "esc clear filter") {
		t.Fatal("the tree filter hid matching containers or omitted its clear shortcut")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.filter != "" || m.logFilter != "keep the log filter" || !m.collapsed["media"] || len(m.rows) != 1 {
		t.Fatal("Escape did not clear just the tree filter and restore the collapsed view")
	}
}
