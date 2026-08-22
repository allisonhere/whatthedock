package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allisonhere/ripple"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/whatthedock/internal/actions"
	"github.com/allisonhere/whatthedock/internal/catalog"
	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/domain"
)

func finishCreateDoneForTest(t *testing.T, model Model, msg createDoneMsg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := model.Update(msg)
	model = updated.(Model)
	for i := 0; model.busy; i++ {
		if i > 30 {
			t.Fatalf("create progress did not finish after %d ticks: pct=%d ready=%v", i, model.actionProgressPercent, model.createDoneReady)
		}
		updated, cmd = model.Update(statusPulseTickMsg{})
		model = updated.(Model)
	}
	return model, cmd
}

func TestCreateOverlayOpensFromShortcutAndRendersPreview(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = updated.(Model)

	if model.overlay != overlayCreate {
		t.Fatalf("overlay = %v, want create", model.overlay)
	}
	view := ansi.Strip(model.View())
	for _, want := range []string{"whatthedock · create", "Compose service", "Standalone container", "services:", "radarr", "ctrl+s validate", "Draft looks good"} {
		if !strings.Contains(view, want) {
			t.Fatalf("create overlay missing %q:\n%s", want, view)
		}
	}
}

func TestCreateCommandPaletteActionOpensOverlay(t *testing.T) {
	model := testModel()

	updated, cmd := model.executeCommand(actions.Create)
	model = updated.(Model)

	if cmd != nil {
		t.Fatalf("create command returned cmd, want nil")
	}
	if model.overlay != overlayCreate {
		t.Fatalf("overlay = %v, want create", model.overlay)
	}
}

func TestCreateOverlayTextFieldsAcceptNavigationLetters(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.moveCreateField(1) // off the Mode tab pseudo-field, onto Project

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	model = updated.(Model)

	if model.createDraft.Project != "l" {
		t.Fatalf("project = %q, want typed l", model.createDraft.Project)
	}
}

// TestCreateValidationAllowsRemoteComposeEditing guards against a
// regression of the "compose editing is local-only" limitation: Compose
// drafts against an SSH system must validate the same as local ones now
// that browsing/writing/applying all have SSH-aware paths.
func TestCreateValidationAllowsRemoteComposeEditing(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34
	model.systems = []config.System{{ID: "remote", Name: "remote", Kind: "ssh", SSHHost: "dock.example", RemoteSocket: "/var/run/docker.sock", LocalSocket: "/tmp/whatthedock.sock"}}
	model.activeSystem = "remote"
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)

	if model.createNoticeErr || !strings.Contains(model.createNotice, "validated") {
		t.Fatalf("createNotice/createNoticeErr = %q/%v, want a successful validation notice", model.createNotice, model.createNoticeErr)
	}
	if strings.Contains(model.status, "validated") {
		t.Fatalf("status = %q, want validation message kept local to the create overlay", model.status)
	}
	if !strings.Contains(ansi.Strip(model.View()), "create draft validated") {
		t.Fatal("create overlay missing local validation notice")
	}
}

func TestCreateStandaloneConfirmsBeforeProviderCreate(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34
	model.openCreateOverlay()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model = updated.(Model)

	if model.createDraft.Mode != createModeStandalone {
		t.Fatalf("mode = %v, want standalone", model.createDraft.Mode)
	}
	if !model.createDraft.Confirming {
		t.Fatalf("confirming = false, want true")
	}
	if len(model.provider.(*fakeProvider).creates) != 0 {
		t.Fatalf("provider create ran before confirmation")
	}
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "whatthedock · confirm create") || !strings.Contains(view, "y create") {
		t.Fatalf("confirm overlay missing expected copy:\n%s", view)
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("confirm returned nil cmd, want create command")
	}
	if !model.busy {
		t.Fatal("busy = false right after dispatching create, want true")
	}
	msg := runCmd(t, cmd).(createDoneMsg)
	model, cmd = finishCreateDoneForTest(t, model, msg)

	if len(model.provider.(*fakeProvider).creates) != 1 {
		t.Fatalf("creates = %d, want 1", len(model.provider.(*fakeProvider).creates))
	}
	if model.overlay != overlayNone || model.statusErr || !strings.Contains(model.status, "created") {
		t.Fatalf("overlay/status/statusErr = %v/%q/%v, want created and closed", model.overlay, model.status, model.statusErr)
	}
	if model.busy {
		t.Fatal("busy = true after createDoneMsg, want false")
	}
	if cmd == nil {
		t.Fatal("create completion returned nil cmd, want refresh")
	}
}

func TestCreateConfirmOverlayRendersProgressWhileBusy(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone
	model.createDraft.Confirming = true
	model.busy = true
	model.actionProgressPercent = 40
	model.actionProgressText = "pulling nginx:alpine — layer abc Downloading 42%"

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "pulling nginx:alpine") {
		t.Fatalf("busy confirm overlay missing action progress:\n%s", view)
	}
	if !strings.Contains(view, "40%") || !strings.ContainsAny(view, "█░") {
		t.Fatalf("busy confirm overlay missing smooth progress bar:\n%s", view)
	}
	if strings.Contains(view, "y create") || strings.Contains(view, "n/esc cancel") {
		t.Fatalf("busy confirm overlay still shows pending confirm/cancel hints:\n%s", view)
	}
}

func TestCreateDoneSuccessWaitsForProgressBarBeforeClosing(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone
	model.createDraft.Confirming = true
	model.busy = true
	model.actionProgressPercent = 40

	updated, cmd := model.Update(createDoneMsg{name: "web"})
	model = updated.(Model)
	if !model.busy || model.overlay != overlayCreate || !model.createDoneReady {
		t.Fatalf("create finalized early: busy=%v overlay=%v ready=%v", model.busy, model.overlay, model.createDoneReady)
	}
	if cmd != nil {
		t.Fatalf("cmd = %#v, want nil while progress bar finishes", cmd)
	}

	for model.busy {
		updated, cmd = model.Update(statusPulseTickMsg{})
		model = updated.(Model)
	}
	if model.busy || model.overlay != overlayNone {
		t.Fatalf("create did not finalize once progress reached 100: busy=%v overlay=%v", model.busy, model.overlay)
	}
	if !strings.Contains(model.status, "created web") {
		t.Fatalf("status = %q, want created web", model.status)
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want refresh after delayed create completion")
	}
}

func TestCreateDoneFailureDoesNotWaitForProgressBar(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone
	model.createDraft.Confirming = true
	model.busy = true
	model.actionProgressPercent = 40

	updated, cmd := model.Update(createDoneMsg{name: "web", err: errors.New("port busy")})
	model = updated.(Model)

	if model.busy || model.createDraft.Confirming {
		t.Fatalf("failure did not return to editable form immediately: busy=%v confirming=%v", model.busy, model.createDraft.Confirming)
	}
	if !model.statusErr || !strings.Contains(model.status, "port busy") {
		t.Fatalf("status/statusErr = %q/%v, want immediate error", model.status, model.statusErr)
	}
	if cmd != nil {
		t.Fatalf("cmd = %#v, want nil on create failure", cmd)
	}
}

func TestCreateConfirmProgressDoesNotMirrorIntoStatusBar(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone
	model.createDraft.Confirming = true
	model.busy = true
	model.status = "Docker connected"
	model.actionProgress = make(chan string, 1)
	model.actionProgress <- "pulling nginx:alpine — layer abc Downloading 42%"

	model.drainActionProgress()

	if !strings.Contains(model.actionProgressText, "pulling nginx:alpine") {
		t.Fatalf("actionProgressText = %q, want create overlay progress", model.actionProgressText)
	}
	if model.status != "Docker connected" {
		t.Fatalf("status = %q, want create progress kept out of the global status bar", model.status)
	}
}

func TestCreateConfirmOverlayRendersHintsWhenNotBusy(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone
	model.createDraft.Confirming = true

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "y create") || !strings.Contains(view, "n/esc cancel") {
		t.Fatalf("non-busy confirm overlay missing confirm/cancel hints:\n%s", view)
	}
}

func TestCreateConfirmIgnoresCancelWhileBusy(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Confirming = true
	model.busy = true
	model.actionProgressText = "creating radarr…"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)

	if cmd != nil {
		t.Fatalf("cmd = %#v, want nil while busy confirm ignores keys", cmd)
	}
	if !model.createDraft.Confirming || model.overlay != overlayCreate || !model.busy {
		t.Fatalf("state changed while busy: overlay=%v confirming=%v busy=%v", model.overlay, model.createDraft.Confirming, model.busy)
	}
}

func TestCreateStandalonePullsImageWhenRequested(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone
	model.createDraft.ContainerName = "web"
	model.createDraft.Image = "nginx:alpine"
	model.createDraft.ImageAction = imageActionPull
	model.createDraft.Confirming = true

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	msg := runCmd(t, cmd).(createDoneMsg)
	if msg.err != nil {
		t.Fatalf("createDoneMsg.err = %v, want nil", msg.err)
	}

	fp := model.provider.(*fakeProvider)
	if len(fp.pulled) != 1 || fp.pulled[0] != "nginx:alpine" {
		t.Fatalf("pulled = %#v, want nginx:alpine pulled before create", fp.pulled)
	}
	if len(fp.creates) != 1 || fp.creates[0].Name != "web" {
		t.Fatalf("creates = %#v, want web created after pull", fp.creates)
	}
	model.drainActionProgress()
	if !strings.Contains(model.actionProgressText, "pulling nginx:alpine") {
		t.Fatalf("actionProgressText = %q, want pull progress", model.actionProgressText)
	}
}

func TestCreateStandalonePullFailureDoesNotCreate(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.provider.(*fakeProvider).pullErr = errors.New("pull failed")
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone
	model.createDraft.ContainerName = "web"
	model.createDraft.Image = "nginx:alpine"
	model.createDraft.ImageAction = imageActionPull
	model.createDraft.Confirming = true

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	msg := runCmd(t, cmd).(createDoneMsg)
	if msg.err == nil || !strings.Contains(msg.err.Error(), "pull failed") {
		t.Fatalf("createDoneMsg.err = %v, want pull failed", msg.err)
	}

	fp := model.provider.(*fakeProvider)
	if len(fp.creates) != 0 {
		t.Fatalf("creates = %#v, want no create after pull failure", fp.creates)
	}
}

// TestCreateFailureKeepsOverlayOpenAndPreservesDraft is the regression
// test for a live report: a create failure (a conflicting port is the
// common one — Docker succeeds at creating the container but fails to
// start it, e.g. "port is already allocated") used to unconditionally
// close the Create overlay, throwing away everything the user had typed
// and dumping them back to the main view with only a terse status-bar
// message to go on. It should stay on the form — dropped back out of the
// confirm step so they can see/fix the offending field and retry —
// with the draft itself untouched.
func TestCreateFailureKeepsOverlayOpenAndPreservesDraft(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34
	model.provider.(*fakeProvider).createErr = errors.New("port is already allocated")
	model.openCreateOverlay()
	model.createDraft.Image = "nginx:alpine"
	model.createDraft.Ports = "8080:80"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model = updated.(Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	msg := runCmd(t, cmd).(createDoneMsg)

	updated, _ = model.Update(msg)
	model = updated.(Model)

	if model.overlay != overlayCreate {
		t.Fatalf("overlay = %v, want overlayCreate to stay open after a failed create", model.overlay)
	}
	if model.createDraft.Confirming {
		t.Fatal("createDraft.Confirming = true, want back to the editable field list, not stuck on the confirm step")
	}
	if model.createDraft.Image != "nginx:alpine" || model.createDraft.Ports != "8080:80" {
		t.Fatalf("createDraft = %+v, want the typed fields preserved", model.createDraft)
	}
	if !model.statusErr || !strings.Contains(model.status, "port is already allocated") {
		t.Fatalf("status/statusErr = %q/%v, want the error surfaced", model.status, model.statusErr)
	}
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "port is already allocated") {
		t.Fatalf("create overlay missing failure near the draft:\n%s", view)
	}
	if model.busy {
		t.Fatal("busy = true after createDoneMsg, want false")
	}
}

func TestCreateStandaloneSpecParsesFields(t *testing.T) {
	draft := createDraft{
		Mode:          createModeStandalone,
		ContainerName: "cache",
		Image:         "redis:7",
		Command:       "redis-server --appendonly yes",
		Ports:         "127.0.0.1:6379:6379/tcp",
		Mounts:        "redis-data:/data:ro",
		Env:           "REDIS_APPENDONLY=yes",
		Restart:       "always",
	}

	spec, err := draft.ContainerSpec()
	if err != nil {
		t.Fatalf("ContainerSpec() error = %v", err)
	}
	if spec.Name != "cache" || spec.Image != "redis:7" || spec.RestartPolicy != "always" || !spec.Start {
		t.Fatalf("spec basics = %#v", spec)
	}
	if got := strings.Join(spec.Command, " "); got != "redis-server --appendonly yes" {
		t.Fatalf("command = %q", got)
	}
	if len(spec.Ports) != 1 || spec.Ports[0].HostIP != "127.0.0.1" || spec.Ports[0].HostPort != 6379 || spec.Ports[0].ContainerPort != 6379 {
		t.Fatalf("ports = %#v", spec.Ports)
	}
	if len(spec.Mounts) != 1 || spec.Mounts[0].Source != "redis-data" || spec.Mounts[0].Destination != "/data" || !spec.Mounts[0].ReadOnly {
		t.Fatalf("mounts = %#v", spec.Mounts)
	}
	if len(spec.Env) != 1 || spec.Env[0] != "REDIS_APPENDONLY=yes" {
		t.Fatalf("env = %#v", spec.Env)
	}
}

func TestCreateStandaloneSpecRejectsInvalidFields(t *testing.T) {
	base := createDraft{
		Mode:          createModeStandalone,
		ContainerName: "cache",
		Image:         "redis:7",
		Restart:       "unless-stopped",
	}
	tests := []struct {
		name string
		edit func(*createDraft)
		want string
	}{
		{
			name: "port",
			edit: func(d *createDraft) { d.Ports = "6379" },
			want: "must be host:container",
		},
		{
			name: "mount",
			edit: func(d *createDraft) { d.Mounts = "/data" },
			want: "must be source:target",
		},
		{
			name: "env",
			edit: func(d *createDraft) { d.Env = "REDIS_APPENDONLY" },
			want: "must be KEY=value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			draft := base
			tt.edit(&draft)
			if _, err := draft.ContainerSpec(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ContainerSpec() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestCreateCatalogSaveLoadRenameDeleteFlow(t *testing.T) {
	dir := t.TempDir()
	model := testModelWithSelectedContainer()
	model.catalogDir = dir
	model.width, model.height = 120, 34
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Project = "dash"
	model.createDraft.Service = "dash"
	model.createDraft.Image = "ghcr.io/allisonhere/dash:latest"
	model.createDraft.Env = "PORT=3939"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(Model)
	if !model.createCatalogOpen {
		t.Fatal("createCatalogOpen = false after ctrl+p in compose mode")
	}
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "whatthedock · compose catalog") || !strings.Contains(view, "save current") {
		t.Fatalf("catalog overlay missing expected UI:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = updated.(Model)
	entries, err := catalog.Load(dir)
	if err != nil {
		t.Fatalf("catalog.Load() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "dash" {
		t.Fatalf("entries = %#v, want saved dash entry", entries)
	}
	if model.createNoticeErr || !strings.Contains(model.createNotice, "saved catalog entry dash") {
		t.Fatalf("createNotice/createNoticeErr = %q/%v, want local catalog save notice", model.createNotice, model.createNoticeErr)
	}
	view = ansi.Strip(model.View())
	if !strings.Contains(view, "saved catalog entry dash") {
		t.Fatalf("catalog overlay missing local save notice:\n%s", view)
	}
	if strings.Contains(model.status, "saved catalog entry") {
		t.Fatalf("status = %q, want catalog save message kept local to overlay", model.status)
	}
	content, err := catalog.Read(dir, entries[0].ID)
	if err != nil {
		t.Fatalf("catalog.Read() error = %v", err)
	}
	if !strings.Contains(content, "ghcr.io/allisonhere/dash:latest") || !strings.Contains(content, "PORT=3939") {
		t.Fatalf("saved content missing draft values:\n%s", content)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated.(Model)
	if model.createCatalogMode != createCatalogRename {
		t.Fatalf("createCatalogMode = %v, want rename", model.createCatalogMode)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	model = updated.(Model)
	for _, r := range "Dash prod" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(Model)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	entries, err = catalog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "Dash prod" {
		t.Fatalf("entries after rename = %#v, want Dash prod", entries)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	if model.createCatalogMode != createCatalogDelete {
		t.Fatalf("createCatalogMode = %v, want delete", model.createCatalogMode)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	entries, err = catalog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries after delete = %#v, want empty", entries)
	}
}

func TestCreateCatalogLoadSingleServicePopulatesDraft(t *testing.T) {
	dir := t.TempDir()
	content := "services:\n  dash:\n    image: ghcr.io/allisonhere/dash:latest\n    restart: unless-stopped\n    environment:\n      - PORT=3939\n"
	if _, err := catalog.Save(dir, "Dash", content); err != nil {
		t.Fatal(err)
	}
	model := testModelWithSelectedContainer()
	model.catalogDir = dir
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Service = "new-service"
	model.createDraft.Image = "image:tag"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	if model.createCatalogOpen {
		t.Fatal("createCatalogOpen = true after loading, want returned to create form")
	}
	if model.createDraft.Service != "dash" || model.createDraft.Image != "ghcr.io/allisonhere/dash:latest" {
		t.Fatalf("Service/Image = %q/%q, want loaded dash template", model.createDraft.Service, model.createDraft.Image)
	}
	if !model.createDraft.OverrideRawSet || !model.createDraft.OverrideRawBase || model.createDraft.OverrideRaw != content {
		t.Fatalf("raw flags/content = %v/%v/%q, want catalog content as raw base", model.createDraft.OverrideRawSet, model.createDraft.OverrideRawBase, model.createDraft.OverrideRaw)
	}
	if !strings.Contains(model.createDraft.Env, "PORT=3939") {
		t.Fatalf("Env = %q, want PORT=3939 from template", model.createDraft.Env)
	}
}

func TestCreateCatalogLoadMultiServiceTemplateBecomesStack(t *testing.T) {
	dir := t.TempDir()
	content := "services:\n  web:\n    image: nginx\n  api:\n    image: httpd\n"
	if _, err := catalog.Save(dir, "Stack", content); err != nil {
		t.Fatal(err)
	}
	model := testModelWithSelectedContainer()
	model.catalogDir = dir
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	if !model.createDraft.IsStack() {
		t.Fatal("IsStack() = false after loading a multi-service catalog template")
	}
	if fields := model.visibleCreateFields(); containsCreateField(fields, createFieldService) {
		t.Fatalf("visible fields = %#v, want stack fields without Service", fields)
	}
}

func TestCreateCatalogDoesNotOpenInStandaloneMode(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.catalogDir = t.TempDir()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(Model)

	if model.createCatalogOpen {
		t.Fatal("createCatalogOpen = true after ctrl+p in standalone mode, want false")
	}
}

func TestImageActionAppearsForNonStackCreateDrafts(t *testing.T) {
	composeModel := testModelWithSelectedContainer()
	composeModel.openCreateOverlay()
	if !containsCreateField(composeModel.visibleCreateFields(), createFieldImageAction) {
		t.Fatal("fresh Compose service visible fields missing Image action")
	}

	standaloneModel := testModelWithSelectedContainer()
	standaloneModel.openCreateOverlay()
	standaloneModel.createDraft.Mode = createModeStandalone
	if !containsCreateField(standaloneModel.visibleCreateFields(), createFieldImageAction) {
		t.Fatal("fresh standalone visible fields missing Image action")
	}

	stackModel := testModelWithSelectedContainer()
	stackModel.openCreateOverlay()
	stackModel.createDraft.Mode = createModeCompose
	stackModel.createDraft.OverrideRaw = "services:\n  web:\n    image: nginx\n  api:\n    image: httpd\n"
	stackModel.createDraft.OverrideRawSet = true
	if containsCreateField(stackModel.visibleCreateFields(), createFieldImageAction) {
		t.Fatal("stack visible fields include Image action, want file-only stack fields")
	}

	standaloneModel.createField = createFieldImageAction
	standaloneModel.cycleCreateChoice(1)
	if standaloneModel.createDraft.ImageAction != imageActionPull {
		t.Fatalf("ImageAction = %q, want %q after cycling", standaloneModel.createDraft.ImageAction, imageActionPull)
	}
}

func TestCreateComposeSpecWritesOverrideBesideBaseFile(t *testing.T) {
	draft := createDraft{
		Mode:        createModeCompose,
		Project:     "media",
		Service:     "Redis Cache",
		Image:       "redis:7",
		Ports:       "6379:6379",
		Mounts:      "redis-data:/data",
		Env:         "REDIS_APPENDONLY=yes",
		Restart:     "unless-stopped",
		ComposeFile: "/srv/media/compose.yml",
	}

	spec, err := draft.ComposeSpec(config.DefaultSystem())
	if err != nil {
		t.Fatalf("ComposeSpec() error = %v", err)
	}
	if spec.Project != "media" || spec.Service != "Redis Cache" || spec.BaseFile != "/srv/media/compose.yml" {
		t.Fatalf("spec basics = %#v", spec)
	}
	if spec.OverrideFile != "/srv/media/compose.whatthedock.redis-cache.yml" {
		t.Fatalf("override = %q", spec.OverrideFile)
	}
	for _, want := range []string{`"Redis Cache":`, `image: "redis:7"`, `- "6379:6379"`, `- "redis-data:/data"`, `- "REDIS_APPENDONLY=yes"`} {
		if !strings.Contains(spec.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, spec.Content)
		}
	}
}

func TestLoadSelectedComposeFilePromotesMultiServiceFileToStackDraft(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	content := "services:\n  web:\n    image: nginx\n  api:\n    image: httpd\n"
	if err := os.WriteFile(base, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.ComposeFile = base

	msg := runCmd(t, model.loadSelectedComposeFileCmd(base)).(createSelectedComposeFileMsg)
	updated, _ := model.Update(msg)
	model = updated.(Model)

	if !model.createDraft.IsStack() {
		t.Fatal("IsStack() = false after loading a multi-service compose file, want true")
	}
	if model.createDraft.OverrideRaw != content || !model.createDraft.OverrideLoaded {
		t.Fatalf("OverrideRaw/Loaded = %q/%v, want selected file content loaded", model.createDraft.OverrideRaw, model.createDraft.OverrideLoaded)
	}
	if fields := model.visibleCreateFields(); !containsCreateField(fields, createFieldComposeFile) || containsCreateField(fields, createFieldService) {
		t.Fatalf("visible fields = %#v, want stack fields without single-service inputs", fields)
	}
}

func TestLoadSelectedComposeFilePopulatesSingleServiceFields(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	content := `services:
  dash:
    # Published by .github/workflows/publish.yml on every push to master.
    build: .
    image: ghcr.io/allisonhere/dash:latest
    container_name: dash
    restart: unless-stopped
    network_mode: host
    environment:
      - PORT=3939
      # Read the bind-mounted omarchy folder below as if it were local.
      - OMARCHY_DIR=/omarchy
    volumes:
      - ./data:/config
      - ./omarchy:/omarchy:ro
      - /var/run/docker.sock:/var/run/docker.sock:ro
`
	if err := os.WriteFile(base, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Service = "new-service"
	model.createDraft.Image = "image:tag"
	model.createDraft.Ports = "8080:80"
	model.createDraft.ComposeFile = base

	msg := runCmd(t, model.loadSelectedComposeFileCmd(base)).(createSelectedComposeFileMsg)
	updated, _ := model.Update(msg)
	model = updated.(Model)

	if !model.createDraft.OverrideRawSet || !model.createDraft.OverrideRawBase || model.createDraft.OverrideRaw != content {
		t.Fatalf("OverrideRawSet/Base/Raw = %v/%v/%q, want selected base file kept as raw editor content", model.createDraft.OverrideRawSet, model.createDraft.OverrideRawBase, model.createDraft.OverrideRaw)
	}
	if model.createDraft.Service != "dash" {
		t.Fatalf("Service = %q, want dash", model.createDraft.Service)
	}
	if model.createDraft.Image != "ghcr.io/allisonhere/dash:latest" || model.createDraft.Restart != "unless-stopped" {
		t.Fatalf("Image/Restart = %q/%q, want dash image/unless-stopped", model.createDraft.Image, model.createDraft.Restart)
	}
	if model.createDraft.Ports != "" {
		t.Fatalf("Ports = %q, want empty because the compose service uses host networking and no ports key", model.createDraft.Ports)
	}
	for _, want := range []string{"PORT=3939", "OMARCHY_DIR=/omarchy"} {
		if !strings.Contains(model.createDraft.Env, want) {
			t.Fatalf("Env = %q, missing %q", model.createDraft.Env, want)
		}
	}
	for _, want := range []string{"./data:/config", "./omarchy:/omarchy:ro", "/var/run/docker.sock:/var/run/docker.sock:ro"} {
		if !strings.Contains(model.createDraft.Mounts, want) {
			t.Fatalf("Mounts = %q, missing %q", model.createDraft.Mounts, want)
		}
	}
}

func TestDefaultApplyComposeCreatePersistsRawSingleServiceBaseExactly(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	var calls []string
	composeCommand = func(_ context.Context, spec composeCreateSpec, args ...string) error {
		calls = append(calls, spec.BaseFile+"|"+spec.OverrideFile+" "+strings.Join(args, " "))
		return nil
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	before := "services:\n  dash:\n    image: old\n"
	if err := os.WriteFile(base, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	content := `services:
  dash:
    # Published by .github/workflows/publish.yml on every push to master, so
    # Portainer can re-pull/update this like any other container.
    build: .
    image: ghcr.io/allisonhere/dash:latest
    container_name: dash
    restart: unless-stopped
    network_mode: host
    environment:
      - PORT=3939
      # Read the bind-mounted omarchy folder below as if it were local.
      - OMARCHY_DIR=/omarchy
    volumes:
      # All data + theme selection live here.
      - ./data:/config
      - ./omarchy:/omarchy:ro
      - /var/run/docker.sock:/var/run/docker.sock:ro
`
	spec := composeCreateSpec{
		Project:      "dash",
		Service:      "dash",
		BaseFile:     base,
		OverrideFile: filepath.Join(dir, "compose.whatthedock.dash.yml"),
		Content:      content,
		FullBase:     true,
	}

	if err := defaultApplyComposeCreate(context.Background(), spec); err != nil {
		t.Fatalf("defaultApplyComposeCreate() error = %v", err)
	}
	written, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != content {
		t.Fatalf("base content was not persisted exactly:\n--- got ---\n%s\n--- want ---\n%s", written, content)
	}
	if len(calls) != 2 || !strings.Contains(calls[0], ".tmp| config") || !strings.Contains(calls[1], "compose.yml| up -d dash") {
		t.Fatalf("compose calls = %#v, want temp config then base-only up -d dash", calls)
	}
}

func TestLoadSelectedComposeFileDoesNotReplaceExistingOverride(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.ComposeFile = "/srv/media/compose.yml"
	model.createDraft.OverrideRaw = "services:\n  radarr:\n    image: radarr:custom\n"
	model.createDraft.OverrideRawSet = true
	model.createDraft.OverrideLoaded = true

	msg := createSelectedComposeFileMsg{
		path:    "/srv/media/compose.yml",
		content: "services:\n  web:\n    image: nginx\n  api:\n    image: httpd\n",
	}
	updated, _ := model.Update(msg)
	model = updated.(Model)

	if model.createDraft.OverrideRaw != "services:\n  radarr:\n    image: radarr:custom\n" {
		t.Fatalf("OverrideRaw was replaced by selected base content:\n%s", model.createDraft.OverrideRaw)
	}
	if model.createDraft.IsStack() {
		t.Fatal("IsStack() = true, want existing per-service override to keep precedence")
	}
}

func containsCreateField(fields []createField, want createField) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}

func TestCreateComposeConfirmsBeforeApply(t *testing.T) {
	original := applyComposeCreate
	defer func() { applyComposeCreate = original }()
	var applied []composeCreateSpec
	applyComposeCreate = func(_ context.Context, spec composeCreateSpec) error {
		applied = append(applied, spec)
		return nil
	}

	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34
	model.openCreateOverlay()
	// The fake radarr fixture's ComposeFile ("/srv/media/compose.yml", see
	// newFakeProvider) doesn't exist on the machine running this test, so
	// openCreateOverlay's new BaseFileMissing check (added for the "adopt
	// out of Portainer" flow) legitimately trips here — reset it so this
	// test still exercises the normal, base-file-already-exists confirm
	// path it's actually about; that new flow has its own dedicated tests.
	model.createDraft.BaseFileMissing = false

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model = updated.(Model)
	if !model.createDraft.Confirming {
		t.Fatalf("confirming = false, want true")
	}
	if len(applied) != 0 {
		t.Fatalf("compose apply ran before confirmation")
	}
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "whatthedock · confirm create") || !strings.Contains(view, "compose.whatthedock") {
		t.Fatalf("confirm overlay missing compose apply copy:\n%s", view)
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("confirm returned nil cmd, want compose apply command")
	}
	if !model.busy {
		t.Fatal("busy = false right after dispatching compose apply, want true")
	}
	msg := runCmd(t, cmd).(createDoneMsg)
	model, cmd = finishCreateDoneForTest(t, model, msg)

	if len(applied) != 1 {
		t.Fatalf("applied = %d, want 1", len(applied))
	}
	if model.overlay != overlayNone || model.statusErr || !strings.Contains(model.status, "created") {
		t.Fatalf("overlay/status/statusErr = %v/%q/%v, want created and closed", model.overlay, model.status, model.statusErr)
	}
	if model.busy {
		t.Fatal("busy = true after createDoneMsg, want false")
	}
	if cmd == nil {
		t.Fatal("compose completion returned nil cmd, want refresh")
	}
}

// TestComposeCreateSelectsNewServiceInTree guards against a regression
// where the Projects tree (the app's left pane) never moved to show a
// newly created/adopted Compose service. Unlike a standalone create,
// applying a Compose service runs an external `docker compose up`, not a
// Docker API call that hands back a container ID synchronously — so
// createDoneMsg had nothing to select, and the tree just kept whatever
// was focused before, which could be a completely different project. This
// simulates what a real `docker compose up -d` leaves behind (a new
// container the next Snapshot() picks up) and checks the tree actually
// selects it once that snapshot lands.
func TestComposeCreateSelectsNewServiceInTree(t *testing.T) {
	original := applyComposeCreate
	defer func() { applyComposeCreate = original }()

	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34
	provider := model.provider.(*fakeProvider)

	applyComposeCreate = func(_ context.Context, spec composeCreateSpec) error {
		newCtr := domain.Container{
			ID:      domain.ResourceID{Host: "local", ID: "sonarr-id"},
			Name:    "sonarr-1",
			Image:   "sonarr",
			State:   domain.StateRunning,
			Compose: domain.ComposeRef{Project: spec.Project, Service: spec.Service},
			Labels:  map[string]string{"com.docker.compose.project": spec.Project, "com.docker.compose.service": spec.Service},
		}
		provider.containers["sonarr-id"] = newCtr
		all := append([]domain.Container{}, provider.snapshot.Standalone...)
		for _, p := range provider.snapshot.Projects {
			for _, s := range p.Services {
				all = append(all, s.Containers...)
			}
		}
		all = append(all, newCtr)
		provider.snapshot = domain.BuildSnapshot(provider.host, all, time.Now())
		return nil
	}

	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.BaseFileMissing = false
	model.createDraft.Project = "media"
	model.createDraft.Service = "sonarr"
	model.createDraft.Image = "sonarr"
	model.createDraft.ComposeFile = "/srv/media/compose.yml"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model = updated.(Model)
	if !model.createDraft.Confirming {
		t.Fatalf("confirming = false, want true")
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	msg := runCmd(t, cmd).(createDoneMsg)

	model, cmd = finishCreateDoneForTest(t, model, msg)
	if cmd == nil {
		t.Fatal("no refresh cmd returned")
	}
	updated, cmd = model.Update(runCmd(t, cmd))
	model = updated.(Model)
	if cmd != nil {
		runCmd(t, cmd)
	}

	if model.selectedID.ID != "sonarr-id" {
		t.Fatalf("selectedID = %v, want sonarr-id — tree did not jump to the newly created compose service", model.selectedID)
	}
	row := model.currentRow()
	if row == nil || row.container == nil || row.container.ID.ID != "sonarr-id" {
		t.Fatalf("cursor row = %+v, want it on the new sonarr container", row)
	}
}

// TestStackConfirmDispatchesStackApplyWithNoServiceArg checks that
// confirming a stack draft (IsStack) calls applyComposeStack — not the
// ordinary single-service applyComposeCreate/applyComposeAdopt — and that
// the spec it's called with has no Service name, matching stackComposeCmd's
// own doc comment (there's no single target to report).
func TestStackConfirmDispatchesStackApplyWithNoServiceArg(t *testing.T) {
	originalCreate, originalStack := applyComposeCreate, applyComposeStack
	defer func() { applyComposeCreate, applyComposeStack = originalCreate, originalStack }()
	var createCalls, stackCalls int
	var stackSpec composeCreateSpec
	applyComposeCreate = func(_ context.Context, _ composeCreateSpec) error {
		createCalls++
		return nil
	}
	applyComposeStack = func(_ context.Context, spec composeCreateSpec) error {
		stackCalls++
		stackSpec = spec
		return nil
	}

	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Project = "media"
	model.createDraft.ComposeFile = "/srv/media/compose.yml"
	model.createDraft.OverrideRaw = "services:\n  web:\n    image: nginx\n  api:\n    image: httpd\n"
	model.createDraft.OverrideRawSet = true
	if !model.createDraft.IsStack() {
		t.Fatal("setup: IsStack() = false, want true")
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model = updated.(Model)
	if !model.createDraft.Confirming {
		t.Fatalf("confirming = false, want true")
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	runCmd(t, cmd)

	if createCalls != 0 {
		t.Fatalf("applyComposeCreate called %d times, want 0 — a stack draft must use applyComposeStack", createCalls)
	}
	if stackCalls != 1 {
		t.Fatalf("applyComposeStack called %d times, want 1", stackCalls)
	}
	if stackSpec.Service != "" {
		t.Fatalf("spec.Service = %q, want empty — a stack has no single service target", stackSpec.Service)
	}
	if stackSpec.Content != model.createDraft.OverrideRaw {
		t.Fatalf("spec.Content = %q, want the full pasted document", stackSpec.Content)
	}
}

// TestStackCreateSelectsProjectRowInTree checks that a stack create/edit
// lands the tree's selection on the project's own row — no single
// service is more "the" result than another, unlike a single-service
// create (TestComposeCreateSelectsNewServiceInTree above).
func TestStackCreateSelectsProjectRowInTree(t *testing.T) {
	original := applyComposeStack
	defer func() { applyComposeStack = original }()
	applyComposeStack = func(context.Context, composeCreateSpec) error { return nil }

	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Project = "media"
	model.createDraft.ComposeFile = "/srv/media/compose.yml"
	model.createDraft.OverrideRaw = "services:\n  web:\n    image: nginx\n  api:\n    image: httpd\n"
	model.createDraft.OverrideRawSet = true

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model = updated.(Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	msg := runCmd(t, cmd).(createDoneMsg)
	if msg.service != "" {
		t.Fatalf("createDoneMsg.service = %q, want empty for a stack", msg.service)
	}

	model, cmd = finishCreateDoneForTest(t, model, msg)
	if model.pendingSelectProject != "media" || model.pendingSelectService != "" {
		t.Fatalf("pendingSelectProject/Service = %q/%q, want media/\"\"", model.pendingSelectProject, model.pendingSelectService)
	}
	if cmd == nil {
		t.Fatal("no refresh cmd returned")
	}
	updated, _ = model.Update(runCmd(t, cmd))
	model = updated.(Model)

	row := model.currentRow()
	if row == nil || row.kind != rowProject || row.project != "media" {
		t.Fatalf("cursor row = %+v, want the media project row", row)
	}
}

// TestCreateComposeAdoptConfirmDispatchesAdoptCmd checks the confirm step's
// wording and dispatch when BaseFileMissing is set: the modal must show the
// adopt-specific explanation (not the ordinary "Write ... and run compose
// up" copy), and confirming with y must call adoptComposeCmd's underlying
// apply (applyComposeAdopt), not the ordinary applyComposeCreate.
func TestCreateComposeAdoptConfirmDispatchesAdoptCmd(t *testing.T) {
	originalCreate, originalAdopt := applyComposeCreate, applyComposeAdopt
	defer func() { applyComposeCreate, applyComposeAdopt = originalCreate, originalAdopt }()
	var createCalls, adoptCalls int
	applyComposeCreate = func(_ context.Context, _ composeCreateSpec) error {
		createCalls++
		return nil
	}
	applyComposeAdopt = func(_ context.Context, _ composeCreateSpec) error {
		adoptCalls++
		return nil
	}

	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34
	model.openCreateOverlay()
	model.createDraft.BaseFileMissing = true

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model = updated.(Model)
	if !model.createDraft.Confirming {
		t.Fatal("confirming = false, want true")
	}
	if !strings.Contains(model.status, "adopt") {
		t.Fatalf("status = %q, want it to mention adopt", model.status)
	}

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "doesn't exist on") || !strings.Contains(view, "create & adopt") {
		t.Fatalf("confirm overlay missing adopt copy:\n%s", view)
	}
	if strings.Contains(view, "and run compose up for") {
		t.Fatalf("confirm overlay still shows the ordinary compose-apply copy:\n%s", view)
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("confirm returned nil cmd, want adopt command")
	}
	if _, ok := runCmd(t, cmd).(createDoneMsg); !ok {
		t.Fatal("adopt confirm did not return a createDoneMsg")
	}
	if adoptCalls != 1 || createCalls != 0 {
		t.Fatalf("adoptCalls/createCalls = %d/%d, want 1/0", adoptCalls, createCalls)
	}
}

func TestCreateFileEntriesShowsDirsAndComposeFilesOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "stack"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"compose.yml", "docker-compose.yaml", "compose.prod.yml", "notes.txt", "values.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := createFileEntries(dir, filepath.Join(dir, "compose.yml"))
	if err != nil {
		t.Fatalf("createFileEntries() error = %v", err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	for _, want := range []string{"..", "stack", "compose.prod.yml", "compose.yml", "docker-compose.yaml"} {
		if !containsString(names, want) {
			t.Fatalf("entries = %#v, missing %q", names, want)
		}
	}
	for _, notWant := range []string{"notes.txt", "values.yaml"} {
		if containsString(names, notWant) {
			t.Fatalf("entries = %#v, unexpectedly included %q", names, notWant)
		}
	}
}

func TestCreateComposeFileBrowserSelectsFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(target, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	model := testModelWithSelectedContainer()
	model.width, model.height = 120, 34
	model.openCreateOverlay()
	model.createDraft.ComposeFile = filepath.Join(dir, "compose.yml")
	model.createField = createFieldComposeFile

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if !model.createBrowsing {
		t.Fatal("createBrowsing = false, want true")
	}
	for i, entry := range model.createFiles {
		if entry.Path == target {
			model.createFileCursor = i
			break
		}
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	if model.createBrowsing {
		t.Fatal("createBrowsing = true after selecting file")
	}
	if model.createDraft.ComposeFile != target {
		t.Fatalf("ComposeFile = %q, want %q", model.createDraft.ComposeFile, target)
	}
}

// TestCreateComposeFileBrowserReloadsExistingOverride guards against a
// regression where picking a compose file through the file browser only
// set ComposeFile and left every other field (Image, Ports, ...) exactly
// as it was before — the same existing-override check openCreateOverlay
// runs at open time never re-ran, so switching to a file whose target
// service already has a WhatTheDock-managed override on disk kept showing
// stale data from whatever had populated the form previously instead of
// loading it.
func TestCreateComposeFileBrowserReloadsExistingOverride(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(dir, "compose.whatthedock.sonarr.yml")
	if err := os.WriteFile(override, []byte("services:\n  sonarr:\n    image: \"sonarr:target\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("media", "sonarr", "/some/stale/compose.yml")
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Service = "sonarr"
	model.createDraft.Image = "adguard/adguardhome" // stale leftover from a previous selection

	model.createBrowsing = true
	model.createFiles = []createFileEntry{{Name: "compose.yml", Path: base}}
	model.createFileCursor = 0

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	if model.createDraft.Image != "sonarr:target" {
		t.Fatalf("Image = %q after picking a compose file with an existing override for sonarr, want sonarr:target", model.createDraft.Image)
	}
}

func TestCreateComposeFileBrowserOpensWithCtrlOFromAnyCreateField(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.ComposeFile = filepath.Join(dir, "compose.yml")
	model.createField = createFieldProject

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(Model)

	if !model.createBrowsing {
		t.Fatal("createBrowsing = false after ctrl+o, want true")
	}
}

// TestCreateComposeFileBrowserOpensWithOFromChoiceField checks where bare
// "o" is still a browse shortcut: choice fields (Mode/Restart) that have no
// other use for a plain letter anyway. See
// TestCreateOTypesNormallyOnTextFields, including the Compose file row
// itself, for the fields where it must NOT hijack typing.
func TestCreateComposeFileBrowserOpensWithOFromChoiceField(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, field := range []createField{createFieldMode, createFieldRestart} {
		t.Run(createFieldLabel(field), func(t *testing.T) {
			model := testModelWithSelectedContainer()
			model.openCreateOverlay()
			model.createDraft.ComposeFile = filepath.Join(dir, "compose.yml")
			model.createField = field

			updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
			model = updated.(Model)

			if !model.createBrowsing {
				t.Fatalf("createBrowsing = false after o on %v, want true", field)
			}
		})
	}
}

// TestCreateOTypesNormallyOnTextFields guards against the bug found while
// testing remote browsing: bare "o" used to open the file browser from ANY
// field in Compose mode, including plain text fields and the Compose file
// row itself — meaning you could never type a value containing the letter
// "o" (like "postgres", "sonarr", or "compose.yml" itself, which contains
// two) without the browser popping open mid-keystroke. Only choice fields
// (Mode/Restart) treat it as a shortcut now; Enter or Ctrl+O still open the
// browser from the Compose file field.
func TestCreateOTypesNormallyOnTextFields(t *testing.T) {
	for _, field := range []createField{createFieldProject, createFieldService, createFieldImage, createFieldPorts, createFieldMounts, createFieldEnv, createFieldComposeFile} {
		t.Run(createFieldLabel(field), func(t *testing.T) {
			model := testModelWithSelectedContainer()
			model.openCreateOverlay()
			model.createDraft.Mode = createModeCompose
			model.createField = field

			updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
			model = updated.(Model)

			if model.createBrowsing {
				t.Fatalf("createBrowsing = true after typing o into %v, want the letter typed instead", field)
			}
		})
	}
}

func TestCreateStandaloneTextFieldsStillAcceptO(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone
	model.createField = createFieldContainerName
	model.createDraft.ContainerName = ""

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	model = updated.(Model)

	if model.createBrowsing {
		t.Fatal("createBrowsing = true in standalone mode, want typed text")
	}
	if model.createDraft.ContainerName != "o" {
		t.Fatalf("ContainerName = %q, want typed o", model.createDraft.ContainerName)
	}
}

func TestCreateFileBrowserOpensFromStandaloneModeViaCtrlO(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone
	model.createDraft.ComposeFile = filepath.Join(dir, "compose.yml")
	model.createField = createFieldContainerName

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(Model)

	if !model.createBrowsing {
		t.Fatal("createBrowsing = false after ctrl+o from standalone mode, want true")
	}
	if model.createDraft.Mode != createModeCompose || model.createField != createFieldComposeFile {
		t.Fatalf("mode/field = %v/%v, want compose/compose-file", model.createDraft.Mode, model.createField)
	}
}

func TestCreateModeTabsSwitchWithBracketKeysFromAnyField(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone
	model.createField = createFieldImage
	model.createDraft.Image = "custom:tag"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	model = updated.(Model)

	if model.createDraft.Mode != createModeCompose {
		t.Fatalf("mode = %v after ], want compose", model.createDraft.Mode)
	}
	if model.createField != createFieldImage {
		t.Fatalf("field = %v after mode switch, want createFieldImage to carry over (shared by both modes)", model.createField)
	}
	if model.createDraft.Image != "custom:tag" {
		t.Fatalf("Image = %q after mode switch, want draft preserved", model.createDraft.Image)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	model = updated.(Model)
	if model.createDraft.Mode != createModeStandalone {
		t.Fatalf("mode = %v after [, want standalone", model.createDraft.Mode)
	}
}

func TestCreateModeTabsResetFieldWhenNotSharedBetweenModes(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createField = createFieldService

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	model = updated.(Model)

	if model.createDraft.Mode != createModeStandalone {
		t.Fatalf("mode = %v after ], want standalone", model.createDraft.Mode)
	}
	standaloneFields := model.visibleCreateFields()
	if model.createField != standaloneFields[0] {
		t.Fatalf("field = %v after switching away from a compose-only field, want reset to first standalone field %v", model.createField, standaloneFields[0])
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestApplyComposeCreateValidatesTempBeforePromote(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	var calls []string
	composeCommand = func(_ context.Context, spec composeCreateSpec, args ...string) error {
		calls = append(calls, spec.OverrideFile+" "+strings.Join(args, " "))
		return nil
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := composeCreateSpec{
		Project:      "media",
		Service:      "cache",
		BaseFile:     base,
		OverrideFile: filepath.Join(dir, "compose.whatthedock.cache.yml"),
		Content:      "services:\n  cache:\n    image: redis:7\n",
	}

	if err := defaultApplyComposeCreate(context.Background(), spec); err != nil {
		t.Fatalf("defaultApplyComposeCreate() error = %v", err)
	}
	if _, err := os.Stat(spec.OverrideFile); err != nil {
		t.Fatalf("override was not promoted: %v", err)
	}
	if _, err := os.Stat(spec.OverrideFile + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp override still exists or stat failed unexpectedly: %v", err)
	}
	if len(calls) != 2 || !strings.Contains(calls[0], ".tmp config") || !strings.Contains(calls[1], "up -d cache") || strings.Contains(calls[1], ".tmp") {
		t.Fatalf("compose calls = %#v", calls)
	}
}

func TestApplyComposeCreatePullsBeforeUpWhenRequested(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	var calls []string
	composeCommand = func(_ context.Context, spec composeCreateSpec, args ...string) error {
		calls = append(calls, spec.OverrideFile+" "+strings.Join(args, " "))
		return nil
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := composeCreateSpec{
		Project:      "media",
		Service:      "cache",
		BaseFile:     base,
		OverrideFile: filepath.Join(dir, "compose.whatthedock.cache.yml"),
		Content:      "services:\n  cache:\n    image: redis:7\n",
		PullBeforeUp: true,
	}
	var progress []string
	spec.Progress = func(line string) { progress = append(progress, line) }

	if err := defaultApplyComposeCreate(context.Background(), spec); err != nil {
		t.Fatalf("defaultApplyComposeCreate() error = %v", err)
	}
	if len(calls) != 3 || !strings.Contains(calls[0], ".tmp config") || !strings.Contains(calls[1], " pull cache") || !strings.Contains(calls[2], " up -d cache") {
		t.Fatalf("compose calls = %#v, want config, pull, up", calls)
	}
	joinedProgress := strings.Join(progress, "\n")
	for _, want := range []string{"validating cache", "writing compose override for cache", "pulling cache", "starting cache"} {
		if !strings.Contains(joinedProgress, want) {
			t.Fatalf("progress = %#v, missing %q", progress, want)
		}
	}
}

// TestDeleteStackRemovesContainersAndBaseFile checks
// defaultApplyComposeDeleteStack's core behavior: `docker compose down`
// (no -v — named volumes are left alone) runs against the whole project,
// then the base compose file itself is deleted — the project-wide
// equivalent of single-service Delete removing that service's own
// definition (defaultApplyComposeDelete, tested above this file).
func TestDeleteStackRemovesContainersAndBaseFile(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	var calls []string
	composeCommand = func(_ context.Context, spec composeCreateSpec, args ...string) error {
		calls = append(calls, spec.BaseFile+" "+strings.Join(args, " "))
		return nil
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	content := "services:\n  web:\n    image: nginx\n  api:\n    image: httpd\n"
	if err := os.WriteFile(base, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := composeCreateSpec{Project: "media", BaseFile: base}

	if err := defaultApplyComposeDeleteStack(context.Background(), spec); err != nil {
		t.Fatalf("defaultApplyComposeDeleteStack() error = %v", err)
	}
	if len(calls) != 1 || !strings.HasSuffix(calls[0], "down") {
		t.Fatalf("compose calls = %#v, want exactly one call ending in \"down\"", calls)
	}
	if strings.Contains(calls[0], "-v") {
		t.Fatalf("compose calls = %#v, want no -v (named volumes must be left alone)", calls)
	}
	if _, err := os.Stat(base); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("base compose file still exists or stat failed unexpectedly: %v", err)
	}
}

// TestDeleteStackRemovesOrphanedPerServiceOverrides checks that any
// compose.whatthedock.<service>.yml override files belonging to the
// project's services get cleaned up too — they'd otherwise be orphaned,
// referencing a base file that no longer exists.
func TestDeleteStackRemovesOrphanedPerServiceOverrides(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	composeCommand = func(context.Context, composeCreateSpec, ...string) error { return nil }

	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	content := "services:\n  web:\n    image: nginx\n  api:\n    image: httpd\n"
	if err := os.WriteFile(base, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	webOverride := filepath.Join(dir, "compose.whatthedock.web.yml")
	if err := os.WriteFile(webOverride, []byte("services:\n  web:\n    image: nginx:custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := composeCreateSpec{Project: "media", BaseFile: base}
	if err := defaultApplyComposeDeleteStack(context.Background(), spec); err != nil {
		t.Fatalf("defaultApplyComposeDeleteStack() error = %v", err)
	}
	if _, err := os.Stat(webOverride); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("web's override file still exists or stat failed unexpectedly: %v", err)
	}
}

func TestDefaultApplyComposeCreateMergesIntoBaseWhenServiceAlreadyDefined(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	var calls []string
	composeCommand = func(_ context.Context, spec composeCreateSpec, args ...string) error {
		calls = append(calls, spec.OverrideFile+" "+strings.Join(args, " "))
		return nil
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	baseContent := "services:\n  cache:\n    image: redis:6 # old\n  web:\n    image: nginx:latest\n"
	if err := os.WriteFile(base, []byte(baseContent), 0o644); err != nil {
		t.Fatal(err)
	}
	overridePath := filepath.Join(dir, "compose.whatthedock.cache.yml")
	if err := os.WriteFile(overridePath, []byte("services:\n  cache:\n    image: redis:6\n"), 0o644); err != nil {
		t.Fatal(err) // a stale override from before "cache" was added to base
	}
	spec := composeCreateSpec{
		Project:      "media",
		Service:      "cache",
		BaseFile:     base,
		OverrideFile: overridePath,
		Content:      "services:\n  cache:\n    image: redis:7\n    restart: \"unless-stopped\"\n",
	}

	if err := defaultApplyComposeCreate(context.Background(), spec); err != nil {
		t.Fatalf("defaultApplyComposeCreate() error = %v", err)
	}
	updated, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	out := string(updated)
	if !strings.Contains(out, "redis:7") || strings.Contains(out, "redis:6") {
		t.Fatalf("base was not merged with the new image:\n%s", out)
	}
	if !strings.Contains(out, "nginx:latest") {
		t.Fatalf("base lost the unrelated service:\n%s", out)
	}
	if _, err := os.Stat(overridePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale override still exists or stat failed unexpectedly: %v", err)
	}
	if len(calls) != 2 || !strings.Contains(calls[0], "config") || !strings.Contains(calls[1], "up -d cache") {
		t.Fatalf("compose calls = %#v, want a base-only config validation then up -d", calls)
	}
	if strings.Contains(calls[1], overridePath) {
		t.Fatalf("final up -d call still references the removed override: %#v", calls)
	}
}

func TestApplyComposeCreateRemovesTempOnValidationFailure(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	composeCommand = func(_ context.Context, _ composeCreateSpec, args ...string) error {
		if len(args) == 1 && args[0] == "config" {
			return errors.New("invalid compose")
		}
		return nil
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := composeCreateSpec{
		Project:      "media",
		Service:      "cache",
		BaseFile:     base,
		OverrideFile: filepath.Join(dir, "compose.whatthedock.cache.yml"),
		Content:      "services:\n  cache:\n    image: redis:7\n",
	}

	err := defaultApplyComposeCreate(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "invalid compose") {
		t.Fatalf("error = %v, want invalid compose", err)
	}
	if _, err := os.Stat(spec.OverrideFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final override exists after failed validation or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(spec.OverrideFile + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp override exists after failed validation or stat failed unexpectedly: %v", err)
	}
}

// TestDefaultApplyComposeAdoptWritesFreshBaseFile covers the "adopt out of
// Portainer" path: BaseFile doesn't exist yet, so spec.Content should be
// written there directly (no merge, no override) and compose invoked with
// just the one -f flag.
func TestDefaultApplyComposeAdoptWritesFreshBaseFile(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	var calls []string
	composeCommand = func(_ context.Context, spec composeCreateSpec, args ...string) error {
		calls = append(calls, spec.BaseFile+"|"+spec.OverrideFile+" "+strings.Join(args, " "))
		return nil
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "docker-compose.yml")
	overridePath := filepath.Join(dir, "compose.whatthedock.telegraf.yml") // must never be touched
	content := "services:\n  telegraf:\n    image: telegraf:1.34\n    restart: \"unless-stopped\"\n"
	spec := composeCreateSpec{
		Project:      "tmp",
		Service:      "telegraf",
		BaseFile:     base,
		OverrideFile: overridePath,
		Content:      content,
	}

	if err := defaultApplyComposeAdopt(context.Background(), spec); err != nil {
		t.Fatalf("defaultApplyComposeAdopt() error = %v", err)
	}

	written, err := os.ReadFile(base)
	if err != nil {
		t.Fatalf("base file was not created: %v", err)
	}
	if string(written) != content {
		t.Fatalf("base content = %q, want %q", written, content)
	}
	if _, err := os.Stat(overridePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an override file was written despite adopting into a fresh base: %v", err)
	}
	if _, err := os.Stat(base + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp base file left behind: %v", err)
	}
	if len(calls) != 2 || !strings.Contains(calls[0], "config") || !strings.Contains(calls[1], "up -d telegraf") {
		t.Fatalf("compose calls = %#v, want a config validation then up -d", calls)
	}
	if strings.Contains(calls[0], overridePath) || strings.Contains(calls[1], overridePath) {
		t.Fatalf("compose calls referenced the override file, want base only: %#v", calls)
	}
}

func TestDefaultApplyComposeAdoptCleansUpTempOnValidationFailure(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	composeCommand = func(_ context.Context, _ composeCreateSpec, args ...string) error {
		if len(args) == 1 && args[0] == "config" {
			return errors.New("invalid compose")
		}
		return nil
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "docker-compose.yml")
	spec := composeCreateSpec{Project: "tmp", Service: "telegraf", BaseFile: base, Content: "services:\n  telegraf:\n    image: telegraf:1.34\n"}

	err := defaultApplyComposeAdopt(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "invalid compose") {
		t.Fatalf("error = %v, want invalid compose", err)
	}
	if _, err := os.Stat(base); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("base file exists after failed validation or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(base + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp base file exists after failed validation or stat failed unexpectedly: %v", err)
	}
}

func TestDefaultApplyComposeDeleteStopsContainerAndRemovesOverride(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	var calls []string
	composeCommand = func(_ context.Context, spec composeCreateSpec, args ...string) error {
		calls = append(calls, spec.OverrideFile+" "+strings.Join(args, " "))
		return nil
	}
	dir := t.TempDir()
	overridePath := filepath.Join(dir, "compose.whatthedock.cache.yml")
	if err := os.WriteFile(overridePath, []byte("services:\n  cache:\n    image: redis:7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := composeCreateSpec{
		Project:      "media",
		Service:      "cache",
		BaseFile:     filepath.Join(dir, "compose.yml"), // never written: not defined in base
		OverrideFile: overridePath,
	}

	if err := defaultApplyComposeDelete(context.Background(), spec); err != nil {
		t.Fatalf("defaultApplyComposeDelete() error = %v", err)
	}
	if _, err := os.Stat(overridePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("override still exists or stat failed unexpectedly: %v", err)
	}
	if len(calls) != 1 || calls[0] != overridePath+" rm -sf cache" {
		t.Fatalf("compose calls = %#v, want a single stop+remove call against the override", calls)
	}
}

func TestDefaultApplyComposeDeleteIsIdempotentWhenOverrideAlreadyGone(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	var calls []string
	composeCommand = func(_ context.Context, spec composeCreateSpec, args ...string) error {
		calls = append(calls, spec.OverrideFile+" "+strings.Join(args, " "))
		return nil
	}
	dir := t.TempDir()
	spec := composeCreateSpec{
		Project:      "media",
		Service:      "cache",
		BaseFile:     filepath.Join(dir, "compose.yml"),
		OverrideFile: filepath.Join(dir, "compose.whatthedock.cache.yml"),
	}

	if err := defaultApplyComposeDelete(context.Background(), spec); err != nil {
		t.Fatalf("defaultApplyComposeDelete() with no existing override, error = %v, want nil", err)
	}
	if len(calls) != 1 || calls[0] != " rm -sf cache" {
		t.Fatalf("compose calls = %#v, want a single stop+remove call with OverrideFile cleared", calls)
	}
}

func TestDefaultApplyComposeDeleteRemovesServiceFromBaseWhenDefined(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	composeCommand = func(context.Context, composeCreateSpec, ...string) error { return nil }
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	baseContent := "services:\n  cache:\n    image: redis:7 # keep siblings\n  web:\n    image: nginx:latest\n"
	if err := os.WriteFile(base, []byte(baseContent), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := composeCreateSpec{
		Project:      "media",
		Service:      "cache",
		BaseFile:     base,
		OverrideFile: filepath.Join(dir, "compose.whatthedock.cache.yml"), // none written
	}

	if err := defaultApplyComposeDelete(context.Background(), spec); err != nil {
		t.Fatalf("defaultApplyComposeDelete() error = %v", err)
	}
	updated, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	out := string(updated)
	if strings.Contains(out, "redis:7") {
		t.Fatalf("base still defines the deleted service:\n%s", out)
	}
	if !strings.Contains(out, "nginx:latest") {
		t.Fatalf("base lost an unrelated service:\n%s", out)
	}
}

func TestDefaultApplyComposeReplicatePullsThenUp(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	var calls []string
	composeCommand = func(_ context.Context, spec composeCreateSpec, args ...string) error {
		calls = append(calls, spec.OverrideFile+" "+strings.Join(args, " "))
		return nil
	}
	dir := t.TempDir()
	overridePath := filepath.Join(dir, "compose.whatthedock.cache.yml")
	if err := os.WriteFile(overridePath, []byte("services:\n  cache:\n    image: redis:7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := composeCreateSpec{
		Project:      "media",
		Service:      "cache",
		BaseFile:     filepath.Join(dir, "compose.yml"),
		OverrideFile: overridePath,
	}

	if err := defaultApplyComposeReplicate(context.Background(), spec); err != nil {
		t.Fatalf("defaultApplyComposeReplicate() error = %v", err)
	}
	if len(calls) != 2 || calls[0] != overridePath+" pull cache" || calls[1] != overridePath+" up -d cache" {
		t.Fatalf("compose calls = %#v, want pull then up -d, both against the existing override", calls)
	}
}

func TestDefaultApplyComposeReplicateOmitsOverrideWhenNoneExists(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	var calls []string
	composeCommand = func(_ context.Context, spec composeCreateSpec, args ...string) error {
		calls = append(calls, spec.OverrideFile+" "+strings.Join(args, " "))
		return nil
	}
	dir := t.TempDir()
	spec := composeCreateSpec{
		Project:      "media",
		Service:      "cache",
		BaseFile:     filepath.Join(dir, "compose.yml"),
		OverrideFile: filepath.Join(dir, "compose.whatthedock.cache.yml"), // never written
	}

	if err := defaultApplyComposeReplicate(context.Background(), spec); err != nil {
		t.Fatalf("defaultApplyComposeReplicate() error = %v", err)
	}
	if len(calls) != 2 || calls[0] != " pull cache" || calls[1] != " up -d cache" {
		t.Fatalf("compose calls = %#v, want pull/up with OverrideFile cleared since none exists", calls)
	}
}

func TestRunDockerComposeOmitsMissingOverrideFile(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	spec := composeCreateSpec{
		Project:  "media",
		Service:  "cache",
		BaseFile: "/srv/media/compose.yml",
		System:   system,
	}

	if err := runDockerCompose(context.Background(), spec, "pull", "cache"); err != nil {
		t.Fatalf("runDockerCompose() error = %v", err)
	}
	want := "docker 'compose' '-p' 'media' '-f' '/srv/media/compose.yml' 'pull' 'cache'"
	if len(fake.calls) != 1 || fake.calls[0] != want {
		t.Fatalf("calls = %#v, want a single call with exactly one -f: %q", fake.calls, want)
	}
}

// TestRunDockerComposeExplainsMissingBaseFile guards against the bug
// reported live: a Compose action (Delete, here) against a container whose
// recorded compose file doesn't actually exist on the host — the common
// case for a stack deployed through Portainer, which manages its compose
// file internally rather than leaving it at the path recorded on the
// container's own labels — silently failed with a raw, easy-to-miss "open
// ...: no such file or directory", leaving the container looking like
// Delete had simply done nothing.
func TestRunDockerComposeExplainsMissingBaseFile(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	spec := composeCreateSpec{
		Project:  "tmp",
		Service:  "telegraf",
		BaseFile: "/tmp/jarvis-portainer-stack.yml",
		System:   system,
	}
	fake.respond(
		"docker 'compose' '-p' 'tmp' '-f' '/tmp/jarvis-portainer-stack.yml' 'rm' '-sf' 'telegraf'",
		"", errors.New("open /tmp/jarvis-portainer-stack.yml: no such file or directory"),
	)

	err := runDockerCompose(context.Background(), spec, "rm", "-sf", "telegraf")
	if err == nil {
		t.Fatal("runDockerCompose() error = nil, want an error explaining the missing base file")
	}
	for _, want := range []string{spec.BaseFile, "jarvis", "Portainer"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, missing %q", err.Error(), want)
		}
	}
}

func TestRunDockerComposePassesThroughUnrelatedErrors(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	spec := composeCreateSpec{
		Project:  "media",
		Service:  "cache",
		BaseFile: "/srv/media/compose.yml",
		System:   system,
	}
	fake.respond(
		"docker 'compose' '-p' 'media' '-f' '/srv/media/compose.yml' 'rm' '-sf' 'cache'",
		"", errors.New("service \"cache\" is not running"),
	)

	err := runDockerCompose(context.Background(), spec, "rm", "-sf", "cache")
	if err == nil || err.Error() != "service \"cache\" is not running" {
		t.Fatalf("error = %v, want the original unrelated error passed through unchanged", err)
	}
}

// fakeSSHRun swaps in for sshRun so remote-Compose tests never shell out to a
// real ssh binary. It records every script (and stdin) it was called with, in
// order, and dispatches canned responses by exact script match.
type fakeSSHRun struct {
	calls     []string
	responses map[string]struct {
		output []byte
		err    error
	}
}

func (f *fakeSSHRun) run(_ context.Context, _ config.System, script string, stdin string) ([]byte, error) {
	call := script
	if stdin != "" {
		call += "\x00stdin=" + stdin
	}
	f.calls = append(f.calls, call)
	if resp, ok := f.responses[script]; ok {
		return resp.output, resp.err
	}
	return nil, nil
}

func (f *fakeSSHRun) respond(script string, output string, err error) {
	if f.responses == nil {
		f.responses = map[string]struct {
			output []byte
			err    error
		}{}
	}
	f.responses[script] = struct {
		output []byte
		err    error
	}{[]byte(output), err}
}

func withFakeSSHRun(t *testing.T) *fakeSSHRun {
	t.Helper()
	fake := &fakeSSHRun{}
	original := sshRun
	sshRun = fake.run
	t.Cleanup(func() { sshRun = original })
	return fake
}

func TestRemoteFileEntriesParsesListing(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	fake.respond("cd '/srv/media-stack' 2>&1 && pwd && ls -1Ap",
		"/srv/media-stack\ncompose.yml\ncompose.override.yml\nREADME.md\nsubdir/\n", nil)

	entries, resolvedDir, err := remoteFileEntries(context.Background(), system, "/srv/media-stack", "/srv/media-stack/compose.override.yml")
	if err != nil {
		t.Fatalf("remoteFileEntries() error = %v", err)
	}
	if resolvedDir != "/srv/media-stack" {
		t.Fatalf("resolvedDir = %q, want /srv/media-stack", resolvedDir)
	}
	if len(entries) != 4 { // .. , subdir/, compose.yml, compose.override.yml (README.md is filtered out)
		t.Fatalf("entries = %#v, want 4 (parent, one dir, two compose files)", entries)
	}
	if entries[0].Name != ".." || !entries[0].Parent {
		t.Fatalf("entries[0] = %#v, want parent entry", entries[0])
	}
	if entries[1].Name != "subdir" || !entries[1].Dir {
		t.Fatalf("entries[1] = %#v, want subdir directory", entries[1])
	}
	names := []string{entries[2].Name, entries[3].Name}
	if names[0] != "compose.override.yml" && names[1] != "compose.override.yml" {
		t.Fatalf("compose files = %v, want compose.override.yml among them", names)
	}
	for _, e := range entries[2:] {
		if e.Name == "compose.override.yml" && !e.Selected {
			t.Fatalf("compose.override.yml not marked selected: %#v", e)
		}
	}
}

func TestRemoteFileEntriesReturnsErrorFromSSH(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	fake.respond("cd '/no/such/dir' 2>&1 && pwd && ls -1Ap", "", errors.New("no such file or directory"))

	_, _, err := remoteFileEntries(context.Background(), system, "/no/such/dir", "")
	if err == nil || !strings.Contains(err.Error(), "no such file or directory") {
		t.Fatalf("err = %v, want the remote error surfaced", err)
	}
}

func TestBrowseCreateDirIsAsyncForSSHSystems(t *testing.T) {
	fake := withFakeSSHRun(t)
	fake.respond("cd '/srv/media-stack' 2>&1 && pwd && ls -1Ap", "/srv/media-stack\ncompose.yml\n", nil)

	model := testModelWithSelectedContainer()
	model.systems = []config.System{{ID: "remote", Name: "jarvis", Kind: "ssh", SSHHost: "jarvis", RemoteSocket: "/var/run/docker.sock", LocalSocket: "/tmp/whatthedock.sock"}}
	model.activeSystem = "remote"

	cmd := model.browseCreateDir("/srv/media-stack")
	if cmd == nil {
		t.Fatal("browseCreateDir() returned nil cmd for an SSH system, want an async listing command")
	}
	if !model.createFileLoading {
		t.Fatal("createFileLoading = false immediately after starting an SSH browse, want true")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("sshRun invoked synchronously (%d calls) — browsing must not block Update", len(fake.calls))
	}

	msg := runCmd(t, cmd).(createFileBrowseMsg)
	updated, _ := model.Update(msg)
	model = updated.(Model)

	if model.createFileLoading {
		t.Fatal("createFileLoading = true after the browse message landed, want false")
	}
	if model.createBrowseDir != "/srv/media-stack" || len(model.createFiles) != 2 { // ".." parent + compose.yml
		t.Fatalf("browseDir/files = %q/%#v after remote listing", model.createBrowseDir, model.createFiles)
	}
}

func TestApplyComposeCreateRemoteWritesAndAppliesOverSSH(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	spec := composeCreateSpec{
		Project:      "media-stack",
		Service:      "cache",
		BaseFile:     "/srv/media-stack/compose.yml",
		OverrideFile: "/srv/media-stack/compose.whatthedock.cache.yml",
		Content:      "services:\n  cache:\n    image: redis:7\n",
		System:       system,
	}

	if err := applyComposeCreateRemote(context.Background(), spec); err != nil {
		t.Fatalf("applyComposeCreateRemote() error = %v", err)
	}

	wantScripts := []string{
		"test -f '/srv/media-stack/compose.yml'",
		// The fake reports this unmocked "cat" as success with empty output,
		// so the base file appears not to already define "cache" and the
		// override path below runs unchanged.
		"cat '/srv/media-stack/compose.yml'",
		"mkdir -p '/srv/media-stack'",
		"cat > '/srv/media-stack/compose.whatthedock.cache.yml.tmp'\x00stdin=" + spec.Content,
		"docker 'compose' '-p' 'media-stack' '-f' '/srv/media-stack/compose.yml' '-f' '/srv/media-stack/compose.whatthedock.cache.yml.tmp' 'config'",
		"mv '/srv/media-stack/compose.whatthedock.cache.yml.tmp' '/srv/media-stack/compose.whatthedock.cache.yml'",
		"docker 'compose' '-p' 'media-stack' '-f' '/srv/media-stack/compose.yml' '-f' '/srv/media-stack/compose.whatthedock.cache.yml' 'up' '-d' 'cache'",
	}
	if len(fake.calls) != len(wantScripts) {
		t.Fatalf("calls = %#v, want %d calls matching %#v", fake.calls, len(wantScripts), wantScripts)
	}
	for i, want := range wantScripts {
		if fake.calls[i] != want {
			t.Fatalf("call %d = %q, want %q", i, fake.calls[i], want)
		}
	}
}

// TestApplyComposeAdoptRemoteWritesAndAppliesOverSSH covers the SSH
// counterpart of the adopt path — writes BaseFile fresh over ssh with no
// preceding read/test-f (the whole point is the file doesn't exist yet)
// and no override flag anywhere in the compose invocations.
func TestApplyComposeAdoptRemoteWritesAndAppliesOverSSH(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	spec := composeCreateSpec{
		Project:      "tmp",
		Service:      "telegraf",
		BaseFile:     "/tmp/jarvis-portainer-stack.yml",
		OverrideFile: "/tmp/compose.whatthedock.telegraf.yml",
		Content:      "services:\n  telegraf:\n    image: telegraf:1.34\n",
		System:       system,
	}

	if err := applyComposeAdoptRemote(context.Background(), spec); err != nil {
		t.Fatalf("applyComposeAdoptRemote() error = %v", err)
	}

	wantScripts := []string{
		"mkdir -p '/tmp'",
		"cat > '/tmp/jarvis-portainer-stack.yml.tmp'\x00stdin=" + spec.Content,
		"docker 'compose' '-p' 'tmp' '-f' '/tmp/jarvis-portainer-stack.yml.tmp' 'config'",
		"mv '/tmp/jarvis-portainer-stack.yml.tmp' '/tmp/jarvis-portainer-stack.yml'",
		"docker 'compose' '-p' 'tmp' '-f' '/tmp/jarvis-portainer-stack.yml' 'up' '-d' 'telegraf'",
	}
	if len(fake.calls) != len(wantScripts) {
		t.Fatalf("calls = %#v, want %d calls matching %#v", fake.calls, len(wantScripts), wantScripts)
	}
	for i, want := range wantScripts {
		if fake.calls[i] != want {
			t.Fatalf("call %d = %q, want %q", i, fake.calls[i], want)
		}
	}
}

func TestApplyComposeCreateRemoteMergesIntoBaseWhenServiceAlreadyDefined(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	spec := composeCreateSpec{
		Project:      "media-stack",
		Service:      "cache",
		BaseFile:     "/srv/media-stack/compose.yml",
		OverrideFile: "/srv/media-stack/compose.whatthedock.cache.yml",
		Content:      "services:\n  cache:\n    image: redis:7\n    restart: \"unless-stopped\"\n",
		System:       system,
	}
	fake.respond("test -f '/srv/media-stack/compose.yml'", "", nil)
	fake.respond("cat '/srv/media-stack/compose.yml'", "services:\n  cache:\n    image: redis:6 # old\n  web:\n    image: nginx:latest\n", nil)

	if err := applyComposeCreateRemote(context.Background(), spec); err != nil {
		t.Fatalf("applyComposeCreateRemote() error = %v", err)
	}

	var wroteTemp, promoted, removedOverride, ranUp bool
	for _, call := range fake.calls {
		switch {
		case strings.HasPrefix(call, "cat > '/srv/media-stack/compose.yml.tmp'"):
			wroteTemp = true
			if !strings.Contains(call, "redis:7") || strings.Contains(call, "redis:6") {
				t.Fatalf("merged temp base has wrong image: %q", call)
			}
			if !strings.Contains(call, "nginx:latest") {
				t.Fatalf("merged temp base lost the unrelated service: %q", call)
			}
		case call == "mv '/srv/media-stack/compose.yml.tmp' '/srv/media-stack/compose.yml'":
			promoted = true
		case call == "rm -f '/srv/media-stack/compose.whatthedock.cache.yml'":
			removedOverride = true
		case strings.Contains(call, "'up' '-d' 'cache'"):
			ranUp = true
			if strings.Contains(call, "compose.whatthedock.cache.yml") {
				t.Fatalf("up -d still references the stale override: %q", call)
			}
		case strings.HasPrefix(call, "cat > '/srv/media-stack/compose.whatthedock.cache.yml"):
			t.Fatalf("wrote an override despite the service already being defined in base: %q", call)
		}
	}
	if !wroteTemp || !promoted || !removedOverride || !ranUp {
		t.Fatalf("calls = %#v, missing one of write-temp/promote/remove-override/up", fake.calls)
	}
}

func TestApplyComposeCreateRemoteCleansUpTempOnValidationFailure(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	spec := composeCreateSpec{
		Project:      "media-stack",
		Service:      "cache",
		BaseFile:     "/srv/media-stack/compose.yml",
		OverrideFile: "/srv/media-stack/compose.whatthedock.cache.yml",
		Content:      "services:\n  cache:\n    image: redis:7\n",
		System:       system,
	}
	fake.respond(
		"docker 'compose' '-p' 'media-stack' '-f' '/srv/media-stack/compose.yml' '-f' '/srv/media-stack/compose.whatthedock.cache.yml.tmp' 'config'",
		"", errors.New("invalid compose"),
	)

	err := applyComposeCreateRemote(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "invalid compose") {
		t.Fatalf("error = %v, want invalid compose", err)
	}
	last := fake.calls[len(fake.calls)-1]
	if last != "rm -f '/srv/media-stack/compose.whatthedock.cache.yml.tmp'" {
		t.Fatalf("last call = %q, want the temp override cleaned up", last)
	}
	for _, call := range fake.calls {
		if strings.HasPrefix(call, "mv ") {
			t.Fatalf("promoted the override despite failed validation: %#v", fake.calls)
		}
	}
}

func TestApplyComposeDeleteRemoteStopsContainerAndRemovesOverride(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	spec := composeCreateSpec{
		Project:      "media-stack",
		Service:      "cache",
		BaseFile:     "/srv/media-stack/compose.yml",
		OverrideFile: "/srv/media-stack/compose.whatthedock.cache.yml",
		System:       system,
	}

	if err := applyComposeDeleteRemote(context.Background(), spec); err != nil {
		t.Fatalf("applyComposeDeleteRemote() error = %v", err)
	}

	// The fake reports every unmocked script as success with empty output,
	// so "test -f" (override exists) and "cat" (base file, empty -> doesn't
	// define the service) both resolve without a registered response.
	wantScripts := []string{
		"test -f '/srv/media-stack/compose.whatthedock.cache.yml'",
		"docker 'compose' '-p' 'media-stack' '-f' '/srv/media-stack/compose.yml' '-f' '/srv/media-stack/compose.whatthedock.cache.yml' 'rm' '-sf' 'cache'",
		"rm -f '/srv/media-stack/compose.whatthedock.cache.yml'",
		"cat '/srv/media-stack/compose.yml'",
	}
	if len(fake.calls) != len(wantScripts) {
		t.Fatalf("calls = %#v, want %#v", fake.calls, wantScripts)
	}
	for i, want := range wantScripts {
		if fake.calls[i] != want {
			t.Fatalf("call %d = %q, want %q", i, fake.calls[i], want)
		}
	}
}

func TestApplyComposeDeleteRemoteRemovesServiceFromBaseWhenDefined(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	spec := composeCreateSpec{
		Project:      "media-stack",
		Service:      "cache",
		BaseFile:     "/srv/media-stack/compose.yml",
		OverrideFile: "/srv/media-stack/compose.whatthedock.cache.yml",
		System:       system,
	}
	fake.respond("test -f '/srv/media-stack/compose.whatthedock.cache.yml'", "", errors.New("no such file"))
	fake.respond("cat '/srv/media-stack/compose.yml'", "services:\n  cache:\n    image: redis:7\n  web:\n    image: nginx:latest\n", nil)

	if err := applyComposeDeleteRemote(context.Background(), spec); err != nil {
		t.Fatalf("applyComposeDeleteRemote() error = %v", err)
	}

	last := fake.calls[len(fake.calls)-1]
	if !strings.HasPrefix(last, "cat > '/srv/media-stack/compose.yml'\x00stdin=") {
		t.Fatalf("last call = %q, want the rewritten base file written back", last)
	}
	if strings.Contains(last, "redis:7") {
		t.Fatalf("rewritten base still defines the deleted service: %q", last)
	}
	if !strings.Contains(last, "nginx:latest") {
		t.Fatalf("rewritten base lost an unrelated service: %q", last)
	}
}

func TestCreateEditorOpensPrefilledWithGeneratedYAML(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)

	if !model.createEditingCompose {
		t.Fatal("createEditingCompose = false after ctrl+y, want true")
	}
	generated := model.createDraft.composeOverrideContent()
	if model.createEditor.Value() != generated {
		t.Fatalf("editor value = %q, want generated override %q", model.createEditor.Value(), generated)
	}
}

func TestCreateEditorIgnoredInStandaloneMode(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeStandalone

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)

	if model.createEditingCompose {
		t.Fatal("createEditingCompose = true after ctrl+y in standalone mode, want false")
	}
}

func TestCreateEditorSaveSetsRawOverrideUsedByComposeSpec(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Project = "media"
	model.createDraft.Service = "custom"
	model.createDraft.ComposeFile = filepath.Join(t.TempDir(), "compose.yml")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("services:\n  custom:\n    image: custom:tag\n")

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)

	if model.createEditingCompose {
		t.Fatal("createEditingCompose = true after ctrl+s, want false")
	}
	if !model.createDraft.OverrideRawSet {
		t.Fatal("OverrideRawSet = false after save, want true")
	}
	want := "services:\n  custom:\n    image: custom:tag"
	if model.createDraft.OverrideRaw != want {
		t.Fatalf("OverrideRaw = %q, want %q", model.createDraft.OverrideRaw, want)
	}

	spec, err := model.createDraft.ComposeSpec(model.activeSystemConfig())
	if err != nil {
		t.Fatal(err)
	}
	if spec.Content != want {
		t.Fatalf("ComposeSpec content = %q, want raw override %q", spec.Content, want)
	}
	if !strings.Contains(model.createDraft.Preview(), "custom:tag") {
		t.Fatalf("Preview() = %q, want it to reflect the hand-edited override", model.createDraft.Preview())
	}
	if model.createNoticeErr || !strings.Contains(model.createNotice, "override YAML edited") {
		t.Fatalf("createNotice/createNoticeErr = %q/%v, want local editor save notice", model.createNotice, model.createNoticeErr)
	}
}

func TestCreateEditorEscCancelsWithoutSettingOverride(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("garbage")

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)

	if model.createEditingCompose {
		t.Fatal("createEditingCompose = true after esc, want false")
	}
	if model.createDraft.OverrideRawSet {
		t.Fatal("OverrideRawSet = true after esc-cancel, want false")
	}
}

func TestCreateEditorSaveWhitespaceOnlyResetsToGenerated(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.OverrideRaw = "services: {}"
	model.createDraft.OverrideRawSet = true

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("   \n  \n")

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)

	if model.createDraft.OverrideRawSet {
		t.Fatal("OverrideRawSet = true after saving a whitespace-only edit, want false (reset to generated)")
	}
}

func TestCreateEditorRippleSubmitAndCancelMsgsRouteToSaveAndCancel(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("services:\n  x:\n    image: x\n")

	updated, _ = model.Update(ripple.SubmitMsg{})
	model = updated.(Model)
	if !model.createDraft.OverrideRawSet || model.createEditingCompose {
		t.Fatalf("after SubmitMsg: OverrideRawSet=%v editing=%v, want true/false", model.createDraft.OverrideRawSet, model.createEditingCompose)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("garbage")
	updated, _ = model.Update(ripple.CancelMsg{})
	model = updated.(Model)
	if model.createEditingCompose {
		t.Fatal("createEditingCompose = true after CancelMsg, want false")
	}
	if model.createDraft.OverrideRaw != "services:\n  x:\n    image: x" {
		t.Fatalf("OverrideRaw changed after a cancelled re-edit: %q", model.createDraft.OverrideRaw)
	}
}

func TestCreateEditorOverlayRendersLargeAndVimStatus(t *testing.T) {
	t.Cleanup(func() { setEditorVimMode(false) })

	model := testModelWithSelectedContainer()
	setEditorVimMode(true) // NewModel resets this to the (false) default settings value; set it after construction.
	model.width, model.height = 140, 44
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "whatthedock · edit override yaml") {
		t.Fatalf("editor overlay missing title:\n%s", view)
	}
	if !strings.Contains(view, "vim") {
		t.Fatalf("editor overlay missing vim status indicator:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	widest := 0
	for _, line := range lines {
		if w := len(line); w > widest {
			widest = w
		}
	}
	if widest < model.width-20 {
		t.Fatalf("editor overlay looks too narrow: widest line %d cols in a %d-col terminal", widest, model.width)
	}
}

func TestApplyOverrideFieldsFromYAMLSyncsFields(t *testing.T) {
	d := createDraft{Service: "radarr", Image: "stale:old", Ports: "stale"}
	content := "services:\n" +
		"  radarr:\n" +
		"    image: \"radarr:custom\"\n" +
		"    restart: \"always\"\n" +
		"    command: \"run --flag\"\n" +
		"    ports:\n" +
		"      - \"7878:7878\"\n" +
		"      - \"7879:7879\"\n" +
		"    volumes:\n" +
		"      - \"/data:/data\"\n" +
		"    environment:\n" +
		"      - \"PUID=1000\"\n"
	d.applyOverrideFieldsFromYAML(content)

	if d.Image != "radarr:custom" {
		t.Fatalf("Image = %q, want radarr:custom", d.Image)
	}
	if d.Restart != "always" {
		t.Fatalf("Restart = %q, want always", d.Restart)
	}
	if d.Command != "run --flag" {
		t.Fatalf("Command = %q, want %q", d.Command, "run --flag")
	}
	if d.Ports != "7878:7878, 7879:7879" {
		t.Fatalf("Ports = %q, want %q", d.Ports, "7878:7878, 7879:7879")
	}
	if d.Mounts != "/data:/data" {
		t.Fatalf("Mounts = %q, want /data:/data", d.Mounts)
	}
	if d.Env != "PUID=1000" {
		t.Fatalf("Env = %q, want PUID=1000", d.Env)
	}
}

func TestApplyOverrideFieldsFromYAMLHandlesMapEnvironment(t *testing.T) {
	d := createDraft{Service: "radarr"}
	content := "services:\n  radarr:\n    image: radarr:custom\n    environment:\n      PUID: 1000\n      TZ: UTC\n"
	d.applyOverrideFieldsFromYAML(content)

	if d.Env != "PUID=1000, TZ=UTC" {
		t.Fatalf("Env = %q, want sorted key=value pairs from map form", d.Env)
	}
}

func TestApplyOverrideFieldsFromYAMLUsesSoleServiceWhenDraftServiceIsBlank(t *testing.T) {
	d := createDraft{Service: ""}
	content := "services:\n  radarr:\n    image: radarr:custom\n"
	d.applyOverrideFieldsFromYAML(content)

	if d.Service != "radarr" || d.Image != "radarr:custom" {
		t.Fatalf("Service/Image = %q/%q, want radarr/radarr:custom", d.Service, d.Image)
	}
}

func TestApplyOverrideFieldsFromYAMLLeavesDraftUnchangedOnParseError(t *testing.T) {
	d := createDraft{Service: "radarr", Image: "kept:as-is"}
	d.applyOverrideFieldsFromYAML("services: [unclosed")

	if d.Image != "kept:as-is" {
		t.Fatalf("Image = %q, want unchanged after a parse error", d.Image)
	}
}

// TestApplyOverrideFieldsFromYAMLDerivesFirstServiceWhenAmbiguous checks
// the fallback for a multi-service paste that names no service matching
// d.Service: rather than leave the form showing stale data guaranteed to
// mismatch the content about to be written (see Validate's own check for
// that failure mode), it derives Service/Image from the first service in
// the document's own source order — sonarr here, since it's listed first,
// not lidarr, and not Go's randomized map order.
func TestApplyOverrideFieldsFromYAMLDerivesFirstServiceWhenAmbiguous(t *testing.T) {
	d := createDraft{Service: "radarr", Image: "kept:as-is"}
	content := "services:\n  sonarr:\n    image: sonarr:latest\n  lidarr:\n    image: lidarr:latest\n"
	d.applyOverrideFieldsFromYAML(content)

	if d.Service != "sonarr" || d.Image != "sonarr:latest" {
		t.Fatalf("Service/Image = %q/%q, want sonarr/sonarr:latest (the first service in source order)", d.Service, d.Image)
	}
}

// TestComposeDraftDetectedAsStackWhenMultipleServicesParsed and
// TestComposeDraftRevertsToSingleServiceWhenTrimmedBackToOne check
// createDraft.IsStack's whole premise: it's derived purely from the
// content currently in OverrideRaw, never stored state — so detecting a
// stack and reverting out of one are both automatic, free side effects
// of what's actually pasted, not something toggled explicitly.
func TestComposeDraftDetectedAsStackWhenMultipleServicesParsed(t *testing.T) {
	d := createDraft{Mode: createModeCompose, OverrideRawSet: true, OverrideRaw: "services:\n  web:\n    image: nginx\n  api:\n    image: httpd\n"}
	if !d.IsStack() {
		t.Fatal("IsStack() = false, want true for a two-service document")
	}
}

func TestComposeDraftRevertsToSingleServiceWhenTrimmedBackToOne(t *testing.T) {
	d := createDraft{Mode: createModeCompose, OverrideRawSet: true, OverrideRaw: "services:\n  web:\n    image: nginx\n  api:\n    image: httpd\n"}
	if !d.IsStack() {
		t.Fatal("setup: IsStack() = false, want true before trimming")
	}
	d.OverrideRaw = "services:\n  web:\n    image: nginx\n"
	if d.IsStack() {
		t.Fatal("IsStack() = true, want false once trimmed back down to one service — nothing should need to be un-set explicitly")
	}
}

// TestApplyOverrideFieldsFromYAMLHandlesExecFormCommand guards against a
// real-world regression the two tests above couldn't catch: composeOverrideDoc
// decoded a service's "command:" strictly as a plain string, so any real
// compose file with even one service using Compose's other allowed form —
// an exec-form list, command: ["sh", "-c", "..."] — made the whole
// yaml.Unmarshal call return an error ("cannot unmarshal !!seq into
// string"), which applyOverrideFieldsFromYAML treated as "nothing to
// derive from" and bailed out immediately, before ever reaching the
// first-service fallback above — even though doc.Services had, in fact,
// already been populated for every service that didn't hit the mismatch.
// This is trimmed from a real file that reproduced it: a mix of
// string-image services and one with an exec-form command.
func TestApplyOverrideFieldsFromYAMLHandlesExecFormCommand(t *testing.T) {
	d := createDraft{Service: "new-service", Image: "image:tag"}
	content := "services:\n" +
		"  web:\n" +
		"    image: nginx:alpine\n" +
		"    ports:\n" +
		"      - \"8080:80\"\n" +
		"  logger:\n" +
		"    image: alpine:latest\n" +
		"    command:\n" +
		"      - sh\n" +
		"      - -c\n" +
		"      - echo hi\n"
	d.applyOverrideFieldsFromYAML(content)

	if d.Service != "web" || d.Image != "nginx:alpine" || d.Ports != "8080:80" {
		t.Fatalf("Service/Image/Ports = %q/%q/%q, want web/nginx:alpine/8080:80 — derivation should survive an exec-form command elsewhere in the document", d.Service, d.Image, d.Ports)
	}
}

// TestValidateCatchesServiceNotDefinedInOverrideContent guards against a
// regression where a draft could confirm and reach `docker compose up`
// with a Service name that the override content it was about to write
// doesn't actually define — the common trigger being
// applyOverrideFieldsFromYAML leaving Service unchanged for an ambiguous
// multi-service paste (see the test above). Before this check, that
// surfaced as a bare, confusing "no such service: <name>" from the
// compose CLI instead of a clear, actionable message in-app.
func TestValidateCatchesServiceNotDefinedInOverrideContent(t *testing.T) {
	// A single-service override — two or more services makes this draft
	// a stack (see createDraft.IsStack), where there's no single Service
	// field to mismatch against at all; that case is covered by
	// TestValidateAllowsStackDraftWithNoSingleService below instead.
	d := createDraft{
		Mode:           createModeCompose,
		Project:        "default",
		Service:        "new-service",
		Image:          "image:tag",
		ComposeFile:    "compose.yml",
		OverrideRaw:    "services:\n  web:\n    image: nginx:alpine\n",
		OverrideRawSet: true,
	}
	err := d.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error — new-service isn't defined in the override content")
	}
	if !strings.Contains(err.Error(), "new-service") || !strings.Contains(err.Error(), "web") {
		t.Fatalf("error = %q, want it to name the missing service and what's actually defined", err.Error())
	}

	d.Service = "web"
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil once Service matches a service the override actually defines", err)
	}
}

// TestValidateAllowsStackDraftWithNoSingleService checks the flip side of
// the test above: once content defines more than one service, the draft
// is a stack (createDraft.IsStack) and Validate must not require Service/
// Image at all — there's no single target for either to name.
func TestValidateAllowsStackDraftWithNoSingleService(t *testing.T) {
	d := createDraft{
		Mode:           createModeCompose,
		Project:        "default",
		ComposeFile:    "compose.yml",
		OverrideRaw:    "services:\n  web:\n    image: nginx:alpine\n  api:\n    image: httpd\n",
		OverrideRawSet: true,
	}
	if !d.IsStack() {
		t.Fatal("IsStack() = false, want true for a two-service override")
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for a stack draft with Project/ComposeFile set", err)
	}
}

// TestSaveCreateEditorWarnsWhenServiceMismatchesOverride checks the same
// mismatch is surfaced immediately at save time (ctrl+s in the raw
// editor), not only much later at confirm.
// TestSaveCreateEditorDerivesServiceFromMultiServicePaste checks that
// pasting a whole multi-service stack into the raw editor and saving no
// longer leaves the Service/Image fields stale — it derives them from the
// first service in the pasted content's own source order, so the form
// (the create overlay's own field column) actually reflects what was
// pasted instead of continuing to show whatever placeholder or prior
// selection had been there.
func TestSaveCreateEditorDerivesServiceFromMultiServicePaste(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Service = "new-service"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("services:\n  web:\n    image: nginx:alpine\n  api:\n    image: httpd\n")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)

	if model.createNoticeErr {
		t.Fatalf("createNoticeErr = true, want false — Service should derive from the pasted content, not stay mismatched: %q", model.createNotice)
	}
	if model.createDraft.Service != "web" || model.createDraft.Image != "nginx:alpine" {
		t.Fatalf("Service/Image = %q/%q, want web/nginx:alpine (the first service in the pasted content)", model.createDraft.Service, model.createDraft.Image)
	}
}

// TestSaveCreateEditorRevalidatesFieldSelectionOnStackTransition guards
// against a regression where m.createField was never re-checked against
// visibleCreateFields() after saving turned the draft into a stack: with
// the cursor left on createFieldService (now hidden — stack mode only
// shows Mode/Project/ComposeFile), the UI had nothing highlighted as
// selected, and further keystrokes silently mutated the hidden Service
// field instead of doing anything visible.
func TestSaveCreateEditorRevalidatesFieldSelectionOnStackTransition(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Service = "new-service"
	model.createField = createFieldService // cursor parked on Service before the paste

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("services:\n  web:\n    image: nginx:alpine\n  api:\n    image: httpd\n")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)

	if !model.createDraft.IsStack() {
		t.Fatal("setup: IsStack() = false, want true after pasting two services")
	}
	visible := model.visibleCreateFields()
	found := false
	for _, f := range visible {
		if f == model.createField {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("createField = %v, not present in visibleCreateFields() = %v — selection points at a hidden field", model.createField, visible)
	}

	// The regression's second symptom: a keystroke after the transition
	// must land on a field the user can actually see, not silently edit
	// the now-hidden Service field.
	beforeService := model.createDraft.Service
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	model = updated.(Model)
	if model.createDraft.Service != beforeService {
		t.Fatalf("Service changed to %q from a keystroke after the field list shrank — it should have landed on a visible field instead", model.createDraft.Service)
	}
}

// TestValidateWarnsIfServiceManuallyRetypedAwayFromOverrideContent checks
// the residual case applyOverrideFieldsFromYAML's own derivation can't
// resolve on its own: the Service field gets hand-edited, after the fact,
// to a name the saved override content doesn't define at all.
func TestValidateWarnsIfServiceManuallyRetypedAwayFromOverrideContent(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("services:\n  web:\n    image: nginx:alpine\n")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)
	if model.createDraft.Service != "web" {
		t.Fatalf("setup: Service = %q, want web after saving the single-service paste", model.createDraft.Service)
	}

	model.createDraft.Service = "totally-different"
	if err := model.createDraft.Validate(); err == nil {
		t.Fatal("Validate() = nil, want an error — Service no longer names anything the saved override content defines")
	}
}

func TestOpenCreateOverlayLoadedOverrideSyncsFormFields(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overrideContent := "services:\n  radarr:\n    image: \"radarr:custom\"\n    ports:\n      - \"7878:7878\"\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.whatthedock.radarr.yml"), []byte(overrideContent), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("media", "radarr", base)
	model.openEditOverlay()

	if model.createDraft.Image != "radarr:custom" {
		t.Fatalf("Image = %q after loading an existing override, want radarr:custom", model.createDraft.Image)
	}
	if model.createDraft.Ports != "7878:7878" {
		t.Fatalf("Ports = %q after loading an existing override, want 7878:7878", model.createDraft.Ports)
	}
}

func TestSavingCreateEditorSyncsFormFieldsFromEditedYAML(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("media", "radarr", base)
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("services:\n  radarr:\n    image: \"radarr:edited\"\n")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)

	if model.createDraft.Image != "radarr:edited" {
		t.Fatalf("Image = %q after hand-editing and saving, want radarr:edited", model.createDraft.Image)
	}
}

func TestCreateOverrideCheckMsgSyncsFormFields(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Service = "radarr"

	overrideContent := "services:\n  radarr:\n    image: \"radarr:custom\"\n"
	updated, _ := model.Update(createOverrideCheckMsg{service: "radarr", content: overrideContent, found: true})
	model = updated.(Model)

	if model.createDraft.Image != "radarr:custom" {
		t.Fatalf("Image = %q after an SSH override-check result, want radarr:custom", model.createDraft.Image)
	}
}

func TestLintComposeYAML(t *testing.T) {
	if err := lintComposeYAML("services:\n  demo:\n    image: demo:latest\n"); err != nil {
		t.Fatalf("lintComposeYAML(valid) = %v, want nil", err)
	}
	if err := lintComposeYAML(""); err != nil {
		t.Fatalf("lintComposeYAML(empty) = %v, want nil", err)
	}
	if err := lintComposeYAML("services:\n  demo:\n\timage: bad tab indent\n"); err == nil {
		t.Fatal("lintComposeYAML(tab-indented) = nil, want a syntax error")
	}
	if err := lintComposeYAML("services: [unclosed"); err == nil {
		t.Fatal("lintComposeYAML(unclosed bracket) = nil, want a syntax error")
	}
}

func TestCreateEditorOverlayShowsLiveLintStatus(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 140, 44
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "valid YAML") {
		t.Fatalf("editor overlay missing valid-YAML lint status for freshly generated content:\n%s", view)
	}

	model.createEditor.SetValue("services: [unclosed")
	view = ansi.Strip(model.View())
	if strings.Contains(view, "valid YAML") {
		t.Fatalf("editor overlay still shows valid YAML for broken content:\n%s", view)
	}
	if !strings.Contains(view, "yaml:") {
		t.Fatalf("editor overlay missing yaml error text for broken content:\n%s", view)
	}
}

func TestCreateModeFieldIsFirstTabStopAndCyclesWithHL(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()

	if model.createField != createFieldMode {
		t.Fatalf("initial field = %v, want createFieldMode as the first tab stop", model.createField)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	model = updated.(Model)
	if model.createDraft.Mode != createModeStandalone {
		t.Fatalf("mode = %v after l on the Mode field, want standalone", model.createDraft.Mode)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	model = updated.(Model)
	if model.createDraft.Mode != createModeCompose {
		t.Fatalf("mode = %v after h on the Mode field, want compose", model.createDraft.Mode)
	}
}

// TestCreateStrayKeyOnModeFieldIsIgnoredNotTypedElsewhere guards against the
// bug being fixed here: landing on the wrong field (or the Mode field,
// before it was Tab-reachable) and pressing a key that isn't a recognized
// shortcut used to silently type that character into whatever field last
// held focus, corrupting it. A key that isn't h/l/enter/[/] while focused on
// Mode must now be a no-op, not a stray character typed anywhere.
func TestCreateStrayKeyOnModeFieldIsIgnoredNotTypedElsewhere(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	before := model.createDraft

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = updated.(Model)

	if model.createDraft != before {
		t.Fatalf("draft changed after a stray '/' on the Mode field: before=%#v after=%#v", before, model.createDraft)
	}
}

// TestDefaultCreateDraftIgnoresSelectedContainer guards against a
// regression back to the old behavior: 'n' used to prefill Image/Project/
// Service/ComposeFile from whatever was currently selected in the tree,
// which made "create something new" indistinguishable from "look at the
// current selection" — a container under the cursor for an unrelated
// reason silently leaked its identity into what was supposed to be an
// unrelated new service. defaultCreateDraft must always be the generic
// blank slate regardless of selection; selectionCreateDraft (below) is
// the prefill-from-selection shape Clone/Edit intentionally use instead.
func TestDefaultCreateDraftIgnoresSelectedContainer(t *testing.T) {
	model := testModelWithSelectedContainer() // container "1": Compose.Service = "radarr"
	draft := model.defaultCreateDraft()
	if draft.Service != "new-service" || draft.Project != "default" || draft.Image != "image:tag" || draft.ComposeFile != "compose.yml" {
		t.Fatalf("draft = %+v, want the generic placeholders regardless of the selected container", draft)
	}
}

func TestSelectionCreateDraftPrefillsFromSelectedContainer(t *testing.T) {
	model := testModelWithSelectedContainer() // container "1": Compose.Service = "radarr"
	draft := model.selectionCreateDraft()
	if draft.Service != "radarr" {
		t.Fatalf("Service = %q, want radarr prefilled from the selected container", draft.Service)
	}
}

// modelSelecting builds a model whose "selected container" is a synthetic
// fixture pointing at a real temp-dir Compose file, so override-detection
// tests can control the exact path instead of the shared fixture's
// hardcoded /srv/media/compose.yml.
func modelSelecting(project, service, composeFile string) Model {
	model := testModel()
	id := domain.ResourceID{Host: "local", ID: "override-test"}
	ctr := domain.Container{
		ID:    id,
		Name:  service + "-1",
		Image: "image:tag",
		Compose: domain.ComposeRef{
			Project:     project,
			Service:     service,
			ConfigFiles: composeFile,
		},
	}
	model.selected = &ctr
	model.selectedID = id
	return model
}

func TestDefaultCloneDraftCarriesPortsMountsEnvRestartCommand(t *testing.T) {
	model := modelSelecting("media", "radarr", "/srv/media/compose.yml")
	model.selected.Ports = []domain.Port{{IP: "0.0.0.0", Private: 7878, Public: 7878, Type: "tcp"}}
	model.selected.Mounts = []domain.Mount{{Source: "/srv/media/radarr", Destination: "/config", ReadWrite: false}}
	model.selected.Env = []string{"PUID=1000", "TZ=UTC"}
	model.selected.RestartPolicy = "always"
	model.selected.Command = "run --flag"

	draft := model.defaultCloneDraft()

	ports, err := parseCreatePorts(draft.Ports)
	if err != nil || len(ports) != 1 || ports[0].ContainerPort != 7878 || ports[0].HostPort != 7878 {
		t.Fatalf("Ports = %q (parsed %#v, err %v), want a single 7878:7878/tcp binding", draft.Ports, ports, err)
	}
	mounts, err := parseCreateMounts(draft.Mounts)
	if err != nil || len(mounts) != 1 || mounts[0].Source != "/srv/media/radarr" || mounts[0].Destination != "/config" || !mounts[0].ReadOnly {
		t.Fatalf("Mounts = %q (parsed %#v, err %v), want a single read-only /srv/media/radarr:/config mount", draft.Mounts, mounts, err)
	}
	if draft.Env != "PUID=1000, TZ=UTC" {
		t.Fatalf("Env = %q, want PUID=1000, TZ=UTC", draft.Env)
	}
	if draft.Restart != "always" {
		t.Fatalf("Restart = %q, want always", draft.Restart)
	}
	if draft.Command != "run --flag" {
		t.Fatalf("Command = %q, want %q", draft.Command, "run --flag")
	}
}

func TestDefaultCloneDraftSuffixesIdentity(t *testing.T) {
	compose := modelSelecting("media", "radarr", "/srv/media/compose.yml")
	if got := compose.defaultCloneDraft(); got.Service != "radarr-clone" {
		t.Fatalf("compose clone Service = %q, want radarr-clone", got.Service)
	}

	standalone := modelSelecting("", "", "")
	standalone.selected.Name = "grafana"
	if got := standalone.defaultCloneDraft(); got.Mode != createModeStandalone || got.ContainerName != "grafana-clone" {
		t.Fatalf("standalone clone Mode/ContainerName = %v/%q, want standalone/grafana-clone", got.Mode, got.ContainerName)
	}
}

func TestOpenCloneOverlaySkipsOverrideDetection(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.whatthedock.radarr.yml"), []byte("services:\n  radarr:\n    image: radarr:custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("media", "radarr", base)
	model.openCloneOverlay()

	if model.createDraft.OverrideRawSet || model.createDraft.OverrideLoaded {
		t.Fatalf("OverrideRawSet/OverrideLoaded = %v/%v, want both false — Clone must not load the original's override", model.createDraft.OverrideRawSet, model.createDraft.OverrideLoaded)
	}
	if model.createDraft.Service != "radarr-clone" {
		t.Fatalf("Service = %q, want radarr-clone", model.createDraft.Service)
	}
}

func TestOpenCreateOverlayLoadsExistingLocalOverride(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overridePath := filepath.Join(dir, "compose.whatthedock.radarr.yml")
	overrideContent := "services:\n  radarr:\n    image: radarr:custom\n"
	if err := os.WriteFile(overridePath, []byte(overrideContent), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("media", "radarr", base)
	cmd := model.openEditOverlay()
	if cmd != nil {
		t.Fatal("openEditOverlay() returned a Cmd for a local system, want synchronous detection (nil)")
	}

	if !model.createDraft.OverrideRawSet || !model.createDraft.OverrideLoaded {
		t.Fatalf("OverrideRawSet/OverrideLoaded = %v/%v, want both true", model.createDraft.OverrideRawSet, model.createDraft.OverrideLoaded)
	}
	if model.createDraft.OverrideRaw != overrideContent {
		t.Fatalf("OverrideRaw = %q, want the existing file's content %q", model.createDraft.OverrideRaw, overrideContent)
	}
	if model.statusErr || !strings.Contains(model.status, "loaded existing override") {
		t.Fatalf("status/statusErr = %q/%v, want a loaded-override confirmation", model.status, model.statusErr)
	}

	spec, err := model.createDraft.ComposeSpec(model.activeSystemConfig())
	if err != nil {
		t.Fatalf("ComposeSpec() error = %v", err)
	}
	if spec.Content != overrideContent {
		t.Fatalf("ComposeSpec content = %q, want the loaded override content, not a regenerated one", spec.Content)
	}
}

func TestOpenEditOverlayLoadsBaseComposeWhenNoOverrideExists(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	content := `services:
  dash:
    image: ghcr.io/allisonhere/dash:latest
    restart: unless-stopped
    network_mode: host
    environment:
      - PORT=3939
      - OMARCHY_DIR=/omarchy
    volumes:
      - ./data:/config
      - ./omarchy:/omarchy:ro
      - /var/run/docker.sock:/var/run/docker.sock:ro
`
	if err := os.WriteFile(base, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("dash", "dash", base)
	cmd := model.openEditOverlay()
	if cmd == nil {
		t.Fatal("openEditOverlay() returned nil, want base compose load command when no override exists")
	}
	msg := runCmd(t, cmd).(createSelectedComposeFileMsg)
	updated, _ := model.Update(msg)
	model = updated.(Model)

	if !model.createDraft.Editing {
		t.Fatal("Editing = false, want modify flow to stay marked as editing")
	}
	if model.createDraft.Service != "dash" || model.createDraft.Image != "ghcr.io/allisonhere/dash:latest" {
		t.Fatalf("Service/Image = %q/%q, want dash compose file values", model.createDraft.Service, model.createDraft.Image)
	}
	if model.createDraft.Ports != "" {
		t.Fatalf("Ports = %q, want empty because the compose file has no ports", model.createDraft.Ports)
	}
	for _, want := range []string{"PORT=3939", "OMARCHY_DIR=/omarchy"} {
		if !strings.Contains(model.createDraft.Env, want) {
			t.Fatalf("Env = %q, missing %q", model.createDraft.Env, want)
		}
	}
	for _, want := range []string{"./data:/config", "./omarchy:/omarchy:ro", "/var/run/docker.sock:/var/run/docker.sock:ro"} {
		if !strings.Contains(model.createDraft.Mounts, want) {
			t.Fatalf("Mounts = %q, missing %q", model.createDraft.Mounts, want)
		}
	}
	if !model.createDraft.OverrideRawSet || !model.createDraft.OverrideRawBase || model.createDraft.OverrideRaw != content {
		t.Fatalf("OverrideRawSet/Base/Raw = %v/%v/%q, want exact base compose content loaded", model.createDraft.OverrideRawSet, model.createDraft.OverrideRawBase, model.createDraft.OverrideRaw)
	}
}

// TestCreateOverrideCheckMsgSetsBaseFileMissingEvenWhenOverrideNotFound
// checks the model.go handler applies baseFileMissing unconditionally —
// not gated behind msg.found the way the override-loading fields are.
func TestCreateOverrideCheckMsgSetsBaseFileMissingEvenWhenOverrideNotFound(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Service = "radarr"

	updated, _ := model.Update(createOverrideCheckMsg{service: "radarr", found: false, baseFileMissing: true})
	model = updated.(Model)

	if !model.createDraft.BaseFileMissing {
		t.Fatal("BaseFileMissing = false after a check reporting the base file missing, want true")
	}
	if model.createDraft.OverrideRawSet {
		t.Fatal("OverrideRawSet = true despite found=false, want false")
	}
}

func TestOpenCreateOverlayLeavesDraftGeneratedWhenNoOverrideExists(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("media", "radarr", base)
	model.openCreateOverlay()

	if model.createDraft.OverrideRawSet || model.createDraft.OverrideLoaded {
		t.Fatalf("OverrideRawSet/OverrideLoaded = %v/%v, want both false when no override file exists", model.createDraft.OverrideRawSet, model.createDraft.OverrideLoaded)
	}
}

// TestOpenCreateOverlayFlagsMissingBaseFileLocally checks the local half of
// the "adopt out of Portainer" detection — a compose-mode draft whose
// already-labeled ComposeFile doesn't exist on disk gets BaseFileMissing
// set the moment the form opens, before the user does anything.
func TestOpenCreateOverlayFlagsMissingBaseFileLocally(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "does-not-exist.yml") // deliberately never written

	model := modelSelecting("tmp", "telegraf", base)
	model.openCreateOverlay()

	if !model.createDraft.BaseFileMissing {
		t.Fatal("BaseFileMissing = false, want true for a nonexistent base file")
	}
}

// TestOpenCreateOverlayDoesNotFlagExistingBaseFile guards the opposite: a
// real, existing base file must never trip BaseFileMissing.
func TestOpenCreateOverlayDoesNotFlagExistingBaseFile(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("media", "radarr", base)
	model.openEditOverlay()

	if model.createDraft.BaseFileMissing {
		t.Fatal("BaseFileMissing = true for a base file that exists, want false")
	}
}

func TestSavingCreateEditorClearsOverrideLoadedFlag(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.whatthedock.radarr.yml"), []byte("services:\n  radarr:\n    image: radarr:custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("media", "radarr", base)
	model.openEditOverlay()
	if !model.createDraft.OverrideLoaded {
		t.Fatal("OverrideLoaded = false after loading an existing override, want true")
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	model = updated.(Model)
	model.createEditor.SetValue("services:\n  radarr:\n    image: radarr:edited\n")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)

	if model.createDraft.OverrideLoaded {
		t.Fatal("OverrideLoaded = true after hand-editing and saving, want false (it's edited now, not just loaded)")
	}
	if !model.createDraft.OverrideRawSet {
		t.Fatal("OverrideRawSet = false after saving an edit, want true")
	}
}

func TestCheckRemoteOverrideCmdFindsExistingOverride(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	overrideContent := "services:\n  radarr:\n    image: radarr:custom\n"
	fake.respond("cat '/srv/media-stack/compose.whatthedock.radarr.yml'", overrideContent, nil)

	cmd := checkRemoteOverrideCmd(system, "/srv/media-stack/compose.yml", "radarr")
	if cmd == nil {
		t.Fatal("checkRemoteOverrideCmd() = nil, want a Cmd")
	}
	msg := runCmd(t, cmd).(createOverrideCheckMsg)
	if !msg.found || msg.content != overrideContent || msg.service != "radarr" {
		t.Fatalf("msg = %#v, want found=true content=%q service=radarr", msg, overrideContent)
	}
}

// TestCheckRemoteOverrideCmdFlagsMissingBaseFile checks the "adopt" flag
// this session added to checkRemoteOverrideCmd: when the ssh `test -f` on
// the base file fails, baseFileMissing comes back true regardless of
// whether an override was found.
func TestCheckRemoteOverrideCmdFlagsMissingBaseFile(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	fake.respond("test -f '/tmp/jarvis-portainer-stack.yml'", "", errors.New("no such file or directory"))

	cmd := checkRemoteOverrideCmd(system, "/tmp/jarvis-portainer-stack.yml", "telegraf")
	msg := runCmd(t, cmd).(createOverrideCheckMsg)
	if !msg.baseFileMissing {
		t.Fatalf("msg = %#v, want baseFileMissing=true", msg)
	}
}

// TestCheckRemoteOverrideCmdBaseFileNotMissingWhenPresent guards the
// opposite: an unmocked `test -f` succeeds by default (withFakeSSHRun's
// documented default), so an ordinary already-existing base file must not
// get flagged.
func TestCheckRemoteOverrideCmdBaseFileNotMissingWhenPresent(t *testing.T) {
	fake := withFakeSSHRun(t)
	system := config.System{Kind: "ssh", SSHHost: "jarvis", Name: "jarvis"}
	fake.respond("test -f '/srv/media-stack/compose.yml'", "", nil)

	cmd := checkRemoteOverrideCmd(system, "/srv/media-stack/compose.yml", "radarr")
	msg := runCmd(t, cmd).(createOverrideCheckMsg)
	if msg.baseFileMissing {
		t.Fatalf("msg = %#v, want baseFileMissing=false", msg)
	}
}

func TestCreateOverrideCheckMsgIgnoredIfServiceChangedBeforeItArrived(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Service = "a-different-service" // user changed it before the ssh round trip landed

	updated, _ := model.Update(createOverrideCheckMsg{service: "radarr", content: "stale content", found: true})
	model = updated.(Model)

	if model.createDraft.OverrideRawSet {
		t.Fatal("a stale override-check result was applied after the draft's service changed")
	}
}

// TestTopbarFitsInOneLine guards against a regression where the topbar's
// content was built to the full box width without leaving room for
// StatusBar's own Padding(0, 1), so lipgloss's Render() silently wrapped
// it onto a second line at every terminal width — pushing the whole app
// down by one row and clipping the bottom row off every screen.
func TestTopbarFitsInOneLine(t *testing.T) {
	for _, width := range []int{40, 80, 120, 160, 220} {
		model := testModelWithSelectedContainer()
		model.width, model.height = width, 34
		view := ansi.Strip(model.View())
		lines := strings.Split(view, "\n")
		if len(lines) != model.height {
			t.Fatalf("width=%d: view height = %d, want exactly %d (topbar likely wrapped)", width, len(lines), model.height)
		}
	}
}

func TestTopbarStackCountMatchesRenderedStackRows(t *testing.T) {
	host := domain.Host{ID: "local", Name: "local"}
	containers := []domain.Container{
		{ID: domain.ResourceID{Host: "local", ID: "1"}, Name: "radarr-1", Image: "radarr", State: domain.StateRunning, Compose: domain.ComposeRef{Project: "media", Service: "radarr"}},
		{ID: domain.ResourceID{Host: "local", ID: "2"}, Name: "jellyfin-1", Image: "jellyfin", State: domain.StateRunning, Compose: domain.ComposeRef{Project: "media", Service: "jellyfin"}},
		{ID: domain.ResourceID{Host: "local", ID: "3"}, Name: "grafana-1", Image: "grafana", State: domain.StateRunning, Compose: domain.ComposeRef{Project: "monitoring", Service: "grafana"}},
		{ID: domain.ResourceID{Host: "local", ID: "4"}, Name: "redis-1", Image: "redis", State: domain.StateRunning, Compose: domain.ComposeRef{Project: "cache", Service: "redis"}},
	}
	model := testModel()
	model.width, model.height = 120, 34
	model.snapshot = domain.BuildSnapshot(host, containers, time.Unix(4, 0))
	model.rows = model.buildRows()

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "Docker connected · 1 stacks · 4 containers") {
		t.Fatalf("topbar did not count only rendered stack rows:\n%s", view)
	}
	if got := rowLabels(model.rows); strings.Join(got, ",") != "redis-1,media,jellyfin-1,radarr-1,grafana-1" {
		t.Fatalf("rows = %#v, want one stack row plus flattened single-container compose apps", got)
	}
}

// TestTopbarCollapsesMultilineErrorStatus guards against a regression
// where a failed docker/compose command's raw combined output — often
// several lines — was passed straight into the topbar's right-side status
// text, which the topbar can only render as a single line. Multi-line
// content reaching alignText/Width().Render() there wrapped the topbar
// onto extra lines the same way the earlier padding bug did, pushing the
// whole app down and clipping the bottom off-screen ("that blip on the
// status line" being unreadable was this same bug's symptom).
func TestTopbarCollapsesMultilineErrorStatus(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 160, 34
	model.status = "create wtd-web: docker compose up failed\nError response from daemon: driver failed programming external connectivity\nBind for 0.0.0.0:8080 failed: port is already allocated"
	model.statusErr = true

	view := ansi.Strip(model.View())
	lines := strings.Split(view, "\n")
	if len(lines) != model.height {
		t.Fatalf("view height = %d, want exactly %d (topbar likely wrapped on the multi-line status)", len(lines), model.height)
	}
	if !strings.Contains(lines[0], "docker compose up failed") {
		t.Fatalf("topbar = %q, want it to show the first line of the error", lines[0])
	}
	if !strings.Contains(lines[0], "app log") {
		t.Fatalf("topbar = %q, want a pointer to the app log for the rest of a multi-line error", lines[0])
	}
}

// TestComposePreviewOverflowDoesNotClipTheOverlay guards against a
// regression where the Create overlay's compose-preview column had no
// height bound at all — a service with enough env vars/labels grew the
// preview taller than the terminal and clipped the whole overlay off the
// top and bottom of the screen instead of being capped and pointing at
// ctrl+y for the full file.
func TestComposePreviewOverflowDoesNotClipTheOverlay(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 160, 34
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Project = "media"
	model.createDraft.Service = "radarr"
	model.createDraft.Image = "lscr.io/linuxserver/radarr:latest"
	model.createDraft.Ports = "7878:7878"
	var envLines []string
	for i := 0; i < 60; i++ {
		envLines = append(envLines, fmt.Sprintf("VAR_%d=value_%d", i, i))
	}
	model.createDraft.Env = strings.Join(envLines, "\n")

	view := ansi.Strip(model.View())
	lines := strings.Split(view, "\n")
	if len(lines) != model.height {
		t.Fatalf("view height = %d, want exactly %d (overlay clipping)", len(lines), model.height)
	}
	if !strings.Contains(view, "more — ctrl+y for the full file") {
		t.Fatal("expected a truncation notice pointing at ctrl+y once the preview overflows the budget")
	}
}

// TestConfirmStepPreviewFitsOverlayBudget guards against the same class of
// bug one screen later: the confirm step (alt+enter from the create form)
// rendered the whole compose preview into RenderSoftBody with no height
// cap of its own. Once the preview was taller than the overlay's real
// budget, tideui's overlay compositor could no longer vertically center
// the box and silently dropped any line landing at/past the terminal's
// bottom edge (placeBoxAt) — cutting off the y/n confirm hints entirely,
// with no error and no visible indication of what happened.
func TestConfirmStepPreviewFitsOverlayBudget(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 160, 34
	model.openCreateOverlay()
	model.createDraft.Mode = createModeCompose
	model.createDraft.Project = "media"
	model.createDraft.Service = "radarr"
	model.createDraft.Image = "lscr.io/linuxserver/radarr:latest"
	model.createDraft.Ports = "7878:7878"
	var envLines []string
	for i := 0; i < 60; i++ {
		envLines = append(envLines, fmt.Sprintf("VAR_%d=value_%d", i, i))
	}
	model.createDraft.Env = strings.Join(envLines, "\n")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model = updated.(Model)
	if !model.createDraft.Confirming {
		t.Fatal("alt+enter should have entered the confirm step")
	}

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "cancel") {
		t.Fatal("cancel hint missing from the rendered confirm screen — the overlay overflowed and got clipped")
	}
}
