package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/whatthedock/internal/clipboard"
	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/domain"
)

// pasteSourceProvider is "Host A": one real-ish container with a secret env
// var, a published port, a named volume, and a custom network — enough to
// exercise every conflict Plan checks once pasted onto a fresh "Host B".
func pasteSourceProvider() *fakeProvider {
	host := domain.Host{ID: "source-host", Name: "Vger"}
	ctr := domain.Container{
		ID:             domain.ResourceID{Host: "source-host", ID: "src-1"},
		Name:           "radarr",
		Image:          "lscr.io/linuxserver/radarr:latest",
		State:          domain.StateRunning,
		RestartPolicy:  "unless-stopped",
		Env:            []string{"PUID=1000", "API_KEY=supersecret123"},
		Ports:          []domain.Port{{IP: "0.0.0.0", Private: 7878, Public: 7878, Type: "tcp"}},
		Mounts:         []domain.Mount{{Type: "volume", Source: "radarr-data", Destination: "/config", ReadWrite: true}},
		Networks:       []string{"media_default"},
		NetworkAliases: map[string][]string{"media_default": {"radarr"}},
		Labels:         map[string]string{},
	}
	snapshot := domain.BuildSnapshot(host, []domain.Container{ctr}, time.Unix(1, 0))
	return &fakeProvider{
		host:       host,
		snapshot:   snapshot,
		containers: map[string]domain.Container{"src-1": ctr},
	}
}

// pasteDestProvider is "Host B": a clean host with none of Host A's images,
// networks, or volumes — so Plan finds the image missing, the network
// missing, and the volume auto-created, exercising every branch at once.
func pasteDestProvider() *fakeProvider {
	host := domain.Host{ID: "dest-host", Name: "Cent"}
	return &fakeProvider{host: host, containers: map[string]domain.Container{}}
}

func modelWithSourceSelected(t *testing.T, source *fakeProvider) Model {
	t.Helper()
	model := testModel()
	model.provider = source
	model.snapshot = source.snapshot
	ctr := source.containers["src-1"]
	model.selectedID = ctr.ID
	model.selected = &ctr
	return model
}

// yankAndSwitch drives "y" on model (whose provider is source), then swaps
// in dest as the current host — the "switch host" step of yank -> switch
// host -> paste, done directly on the field the way a systemSwitchMsg
// would (see model.go's own case), without needing a real provider
// factory.
func yankAndSwitch(t *testing.T, model Model, dest *fakeProvider) Model {
	t.Helper()
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	msg := runCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)
	if model.clipboard.Len() != 1 {
		t.Fatalf("clipboard.Len() = %d, want 1 after yank", model.clipboard.Len())
	}
	model.provider = dest
	return model
}

func openPasteReview(t *testing.T, model Model) Model {
	t.Helper()
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	model = updated.(Model)
	msg := runCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)
	if model.overlay != overlayPaste {
		t.Fatalf("overlay = %v, want overlayPaste", model.overlay)
	}
	return model
}

func TestYankCapturesSelectedContainer(t *testing.T) {
	model := modelWithSourceSelected(t, pasteSourceProvider())
	model = yankAndSwitch(t, model, pasteDestProvider())

	current, ok := model.clipboard.Current()
	if !ok {
		t.Fatal("clipboard empty after yank")
	}
	if current.Name != "radarr" || current.Image != "lscr.io/linuxserver/radarr:latest" {
		t.Fatalf("yanked = %#v, want radarr/lscr.io/linuxserver/radarr:latest", current)
	}
	if current.RestartPolicy != "unless-stopped" {
		t.Fatalf("RestartPolicy = %q", current.RestartPolicy)
	}
	found := false
	for _, e := range current.Env {
		if e.Key == "API_KEY" {
			found = true
			if e.Value != "supersecret123" {
				t.Fatal("API_KEY value altered during yank — clipboard must preserve real values")
			}
			if !e.Secret {
				t.Fatal("API_KEY not flagged secret after yank")
			}
		}
	}
	if !found {
		t.Fatal("API_KEY missing from yanked env")
	}
}

func TestPasteWithEmptyClipboardShowsStatusMessage(t *testing.T) {
	model := testModel()
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	model = updated.(Model)
	msg := runCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	if model.overlay == overlayPaste {
		t.Fatal("overlay = overlayPaste, want it to stay closed with an empty clipboard")
	}
	if !model.statusErr || !strings.Contains(model.status, "yank") {
		t.Fatalf("status/statusErr = %q/%v, want an error mentioning yanking first", model.status, model.statusErr)
	}
}

func TestPasteReviewScreenShowsConflictsAndMasksSecret(t *testing.T) {
	model := modelWithSourceSelected(t, pasteSourceProvider())
	model = yankAndSwitch(t, model, pasteDestProvider())
	model = openPasteReview(t, model)

	if model.pastePlan == nil {
		t.Fatal("pastePlan is nil after opening the review screen")
	}
	plan := *model.pastePlan
	if !plan.NeedsPull {
		t.Fatal("NeedsPull = false, want true: the image isn't on the fresh destination")
	}
	if len(plan.NetworksToCreate) != 1 || plan.NetworksToCreate[0] != "media_default" {
		t.Fatalf("NetworksToCreate = %#v, want [media_default]", plan.NetworksToCreate)
	}
	if plan.Blocked() {
		t.Fatalf("Blocked() = true on a clean destination: %#v", plan.Conflicts)
	}

	model.width, model.height = 100, 40
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "secret-like") {
		t.Fatalf("review screen missing the secret-env conflict line:\n%s", view)
	}
	if strings.Contains(view, "supersecret123") {
		t.Fatal("review screen leaked the raw secret value")
	}
}

func TestPasteEnterOpensPrefilledCreateForm(t *testing.T) {
	model := modelWithSourceSelected(t, pasteSourceProvider())
	model = yankAndSwitch(t, model, pasteDestProvider())
	model = openPasteReview(t, model)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	if model.overlay != overlayCreate {
		t.Fatalf("overlay = %v, want overlayCreate after Enter on the review screen", model.overlay)
	}
	if !model.createDraft.Pasting || model.createDraft.PastePlan == nil {
		t.Fatal("createDraft is not marked as a paste draft")
	}
	if model.createDraft.ContainerName != "radarr" || model.createDraft.Image != "lscr.io/linuxserver/radarr:latest" {
		t.Fatalf("prefilled name/image = %q/%q", model.createDraft.ContainerName, model.createDraft.Image)
	}
	if model.createDraft.Networks != "media_default" {
		t.Fatalf("prefilled Networks = %q, want media_default", model.createDraft.Networks)
	}
	if model.pastePlan != nil {
		t.Fatal("pastePlan should be cleared once the plan has been handed to the create draft")
	}
}

func TestPasteEscCancelsAndKeepsClipboardItem(t *testing.T) {
	model := modelWithSourceSelected(t, pasteSourceProvider())
	model = yankAndSwitch(t, model, pasteDestProvider())
	model = openPasteReview(t, model)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)

	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v, want overlayNone after Esc", model.overlay)
	}
	if model.clipboard.Len() != 1 {
		t.Fatal("Esc discarded the clipboard item — paste is a copy, not a cut")
	}
}

func TestPasteDeployRefusedWhenBlocked(t *testing.T) {
	dest := pasteDestProvider()
	// A container already named "radarr" on the destination — a blocking
	// name collision (see clipboard.Plan).
	collision := domain.Container{ID: domain.ResourceID{Host: "dest-host", ID: "existing"}, Name: "radarr"}
	dest.containers["existing"] = collision
	dest.snapshot = domain.BuildSnapshot(dest.host, []domain.Container{collision}, time.Unix(1, 0))

	model := modelWithSourceSelected(t, pasteSourceProvider())
	model = yankAndSwitch(t, model, dest)
	model = openPasteReview(t, model)

	if !model.pastePlan.Blocked() {
		t.Fatal("plan.Blocked() = false, want true for a name collision")
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("'d' on a blocked plan returned a cmd, want none — it must not deploy")
	}
	if model.overlay != overlayPaste {
		t.Fatalf("overlay = %v, want it to stay on overlayPaste when blocked", model.overlay)
	}
	if !model.statusErr || !strings.Contains(model.status, "cannot deploy") {
		t.Fatalf("status/statusErr = %q/%v, want a clear refusal", model.status, model.statusErr)
	}
}

// TestYankThenPasteAcrossHostsDeploysEquivalentContainer is the
// end-to-end "Host A -> yank -> portable model -> paste planner -> Host B
// equivalent container" flow: yank on the source fakeProvider, switch to a
// clean destination fakeProvider, review, and deploy with "d" — asserting
// the destination's recorded CreateContainer spec matches the source
// container's shape, and that the missing network got created first.
func TestYankThenPasteAcrossHostsDeploysEquivalentContainer(t *testing.T) {
	dest := pasteDestProvider()
	model := modelWithSourceSelected(t, pasteSourceProvider())
	model = yankAndSwitch(t, model, dest)
	model = openPasteReview(t, model)

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	if model.overlay != overlayCreate || !model.createDraft.Confirming {
		t.Fatalf("overlay/confirming = %v/%v, want overlayCreate confirming after 'd'", model.overlay, model.createDraft.Confirming)
	}
	if cmd != nil {
		t.Fatal("'d' itself should only open the confirm step, not deploy yet")
	}

	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	msg := runCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	if model.statusErr {
		t.Fatalf("paste failed: %s", model.status)
	}
	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v, want overlayNone after a successful paste", model.overlay)
	}
	if !strings.Contains(model.status, "pasted") {
		t.Fatalf("status = %q, want it to say \"pasted\"", model.status)
	}

	if len(dest.createdNetworks) != 1 || dest.createdNetworks[0] != "media_default" {
		t.Fatalf("createdNetworks = %#v, want [media_default] created before the container", dest.createdNetworks)
	}
	if len(dest.pulled) != 1 || dest.pulled[0] != "lscr.io/linuxserver/radarr:latest" {
		t.Fatalf("pulled = %#v, want the source image pulled once", dest.pulled)
	}
	if len(dest.creates) != 1 {
		t.Fatalf("creates = %#v, want exactly one", dest.creates)
	}
	spec := dest.creates[0]
	if spec.Name != "radarr" || spec.Image != "lscr.io/linuxserver/radarr:latest" {
		t.Fatalf("created name/image = %q/%q", spec.Name, spec.Image)
	}
	if spec.RestartPolicy != "unless-stopped" {
		t.Fatalf("created RestartPolicy = %q", spec.RestartPolicy)
	}
	if len(spec.Ports) != 1 || spec.Ports[0].HostPort != 7878 || spec.Ports[0].ContainerPort != 7878 {
		t.Fatalf("created Ports = %#v", spec.Ports)
	}
	if len(spec.Mounts) != 1 || spec.Mounts[0].Source != "radarr-data" {
		t.Fatalf("created Mounts = %#v", spec.Mounts)
	}
	if len(spec.Networks) != 1 || spec.Networks[0].Name != "media_default" || spec.Networks[0].Aliases[0] != "radarr" {
		t.Fatalf("created Networks = %#v", spec.Networks)
	}
	wantEnv := map[string]bool{"PUID=1000": true, "API_KEY=supersecret123": true}
	if len(spec.Env) != len(wantEnv) {
		t.Fatalf("created Env = %#v", spec.Env)
	}
	for _, e := range spec.Env {
		if !wantEnv[e] {
			t.Fatalf("unexpected env entry %q", e)
		}
	}
}

// TestPasteCreatesNetworkAddedViaReviewFormEdit is a regression test for a
// real bug found in review: clipboard.Plan computes PastePlan.NetworksToCreate
// once, before the review form is even opened. Editing the Networks field
// on the "Enter -> review/fix" path changes the deployed spec's networks,
// but pasteApplyCmd used to still create only the plan's original
// (by-then-stale) list — a renamed/added network would never get created,
// and CreateContainer would fail with a raw "network not found" from
// Docker. Fixed by having pasteApplyCmd recompute what's missing against
// the actual final spec and the destination's current networks, right
// before creating anything.
func TestPasteCreatesNetworkAddedViaReviewFormEdit(t *testing.T) {
	dest := pasteDestProvider()
	model := modelWithSourceSelected(t, pasteSourceProvider())
	model = yankAndSwitch(t, model, dest)
	model = openPasteReview(t, model)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.overlay != overlayCreate || !model.createDraft.Pasting {
		t.Fatalf("overlay/pasting = %v/%v after Enter, want overlayCreate/true", model.overlay, model.createDraft.Pasting)
	}

	// Replace the plan's original network with one the plan never saw —
	// exactly what editing this field on the review form does.
	model.createDraft.Networks = "custom_net"
	model.createField = createFieldNetworks
	model.syncCreateFieldEditor()

	if !model.validateCreateDraft() {
		t.Fatalf("validateCreateDraft() = false, want true: %s", model.createNotice)
	}
	model.createDraft.Confirming = true

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	msg := runCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	if model.statusErr {
		t.Fatalf("paste failed: %s", model.status)
	}
	if len(dest.createdNetworks) != 1 || dest.createdNetworks[0] != "custom_net" {
		t.Fatalf("createdNetworks = %#v, want [custom_net] (the edited network, not the plan's original media_default)", dest.createdNetworks)
	}
	if len(dest.creates) != 1 || len(dest.creates[0].Networks) != 1 || dest.creates[0].Networks[0].Name != "custom_net" {
		t.Fatalf("created spec networks = %#v, want [custom_net]", dest.creates[0].Networks)
	}
}

func TestPreparePastePlanAppendsBindPathConflict(t *testing.T) {
	source := pasteSourceProvider()
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	ctr := source.containers["src-1"]
	ctr.Mounts = append(ctr.Mounts, domain.Mount{Type: "bind", Source: missing, Destination: "/data", ReadWrite: true})
	source.containers["src-1"] = ctr
	source.snapshot = domain.BuildSnapshot(source.host, []domain.Container{ctr}, time.Unix(1, 0))

	model := modelWithSourceSelected(t, source)
	model = yankAndSwitch(t, model, pasteDestProvider())
	model = openPasteReview(t, model)

	found := false
	for _, c := range model.pastePlan.Conflicts {
		if c.Kind == "bind-path" && c.Detail == missing {
			found = true
			if c.Severity != clipboard.SeverityBlock {
				t.Fatalf("bind-path conflict severity = %q, want blocking — a missing bind source is a guaranteed Docker create-time failure, not a deployable-past warning (see BindPathConflict's doc comment)", c.Severity)
			}
		}
	}
	if !found {
		t.Fatalf("conflicts = %#v, want a bind-path conflict for %q", model.pastePlan.Conflicts, missing)
	}
	if !model.pastePlan.Blocked() {
		t.Fatal("Blocked() = false, want true: a missing bind mount source must refuse 'd' deploy")
	}
}

// TestPastePreservesEnvValueContainingComma is a regression test for a
// real bug found in review: an env var whose value contains a comma
// (JSON, a CSV list, a connection string — all common) used to be
// corrupted the moment it round-tripped through the review form's
// comma-joined Env text field. "APP_OPTS=a,b,c" would come back as three
// fragments ("APP_OPTS=a", "b", "c"), the last two of which fail
// validation ("must be KEY=value") — silently dropping the whole env var,
// or worse, becoming their own bogus vars if a fragment happened to
// contain its own "=". Fixed by quoting (formatEnvEntries/splitEnvEntries)
// any entry that needs it instead of a plain comma split/join.
func TestPastePreservesEnvValueContainingComma(t *testing.T) {
	source := pasteSourceProvider()
	ctr := source.containers["src-1"]
	ctr.Env = append(ctr.Env, "APP_OPTS=a,b,c")
	source.containers["src-1"] = ctr
	source.snapshot = domain.BuildSnapshot(source.host, []domain.Container{ctr}, time.Unix(1, 0))

	dest := pasteDestProvider()
	model := modelWithSourceSelected(t, source)
	model = yankAndSwitch(t, model, dest)
	model = openPasteReview(t, model)

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	msg := runCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	if model.statusErr {
		t.Fatalf("paste failed: %s", model.status)
	}
	if len(dest.creates) != 1 {
		t.Fatalf("creates = %#v, want exactly one", dest.creates)
	}
	found := false
	for _, e := range dest.creates[0].Env {
		if e == "APP_OPTS=a,b,c" {
			found = true
		}
		if e == "b" || e == "c" {
			t.Fatalf("Env = %#v contains a fragment of a corrupted comma-bearing value", dest.creates[0].Env)
		}
	}
	if !found {
		t.Fatalf("Env = %#v, want APP_OPTS=a,b,c intact", dest.creates[0].Env)
	}
}

// TestPastePreservesCommandArgumentContainingSpace is a regression test for
// a real bug found in review: domain.Container.Command/Entrypoint is a
// single space-joined display string, but a plain-space split/join is lossy
// for any argument that itself contains a space — ["sh","-c","echo hi"]
// joined as "sh -c echo hi" and split back on whitespace comes back as four
// arguments ("sh", "-c", "echo", "hi") instead of three. Fixed by quoting
// (domain.JoinShellWords/SplitShellWords) any argument that needs it,
// mirroring the same fix already applied for comma-bearing env values (see
// TestPastePreservesEnvValueContainingComma).
func TestPastePreservesCommandArgumentContainingSpace(t *testing.T) {
	source := pasteSourceProvider()
	ctr := source.containers["src-1"]
	ctr.Command = domain.JoinShellWords([]string{"sh", "-c", "echo hi"})
	source.containers["src-1"] = ctr
	source.snapshot = domain.BuildSnapshot(source.host, []domain.Container{ctr}, time.Unix(1, 0))

	dest := pasteDestProvider()
	model := modelWithSourceSelected(t, source)
	model = yankAndSwitch(t, model, dest)
	model = openPasteReview(t, model)

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	msg := runCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	if model.statusErr {
		t.Fatalf("paste failed: %s", model.status)
	}
	if len(dest.creates) != 1 {
		t.Fatalf("creates = %#v, want exactly one", dest.creates)
	}
	want := []string{"sh", "-c", "echo hi"}
	got := dest.creates[0].Command
	if len(got) != len(want) {
		t.Fatalf("Command = %#v, want %#v (a space-containing argument must survive as one argument, not split)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Command = %#v, want %#v", got, want)
		}
	}
}

// TestRedirectMissingBindMountsUnblocksLocalDeploy drives "t" end to end
// against a local destination: a bind-path conflict that would otherwise
// refuse "d" gets redirected to a real placeholder directory this test can
// see on disk, labeled with the original path, and the paste then deploys
// successfully.
func TestRedirectMissingBindMountsUnblocksLocalDeploy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	source := pasteSourceProvider()
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	ctr := source.containers["src-1"]
	ctr.Mounts = append(ctr.Mounts, domain.Mount{Type: "bind", Source: missing, Destination: "/config-extra", ReadWrite: true})
	source.containers["src-1"] = ctr
	source.snapshot = domain.BuildSnapshot(source.host, []domain.Container{ctr}, time.Unix(1, 0))

	dest := pasteDestProvider()
	model := modelWithSourceSelected(t, source)
	model = yankAndSwitch(t, model, dest)
	model = openPasteReview(t, model)

	if !model.pastePlan.Blocked() {
		t.Fatal("Blocked() = false, want true before redirecting")
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	model = updated.(Model)
	msg := runCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	if model.statusErr {
		t.Fatalf("redirect failed: %s", model.status)
	}
	if model.pastePlan.Blocked() {
		t.Fatalf("Blocked() = true, want false after redirecting: %#v", model.pastePlan.Conflicts)
	}
	var placeholder string
	for _, m := range model.pastePlan.Spec.Mounts {
		if m.Destination == "/config-extra" {
			placeholder = m.Source
		}
	}
	if placeholder == "" || placeholder == missing {
		t.Fatalf("mount source = %q, want it redirected away from %q", placeholder, missing)
	}
	if info, err := os.Stat(placeholder); err != nil || !info.IsDir() {
		t.Fatalf("placeholder directory %q does not exist on disk: %v", placeholder, err)
	}
	if got := model.pastePlan.Spec.Labels[clipboard.BindRedirectLabelPrefix+"/config-extra"]; got != missing {
		t.Fatalf("label = %q, want the original path %q", got, missing)
	}

	// A second "t" press finds nothing left to redirect.
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("second t press returned a cmd, want an immediate no-op (nothing to redirect)")
	}
	if model.statusErr || !strings.Contains(model.status, "nothing to redirect") {
		t.Fatalf("status/statusErr = %q/%v, want an informational 'nothing to redirect'", model.status, model.statusErr)
	}

	// The previously-blocking deploy now proceeds.
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	msg = runCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)
	if model.statusErr {
		t.Fatalf("paste failed after redirect: %s", model.status)
	}
	if len(dest.creates) != 1 {
		t.Fatalf("creates = %#v, want exactly one", dest.creates)
	}
	if dest.creates[0].Labels[clipboard.BindRedirectLabelPrefix+"/config-extra"] != missing {
		t.Fatalf("created container's labels = %#v, want the original path preserved", dest.creates[0].Labels)
	}
}

// TestRedirectMissingBindMountsShowsConfirmReminder is a regression test
// for the "restate it right before deploying" addition: a one-time status
// line after "t" is easy to lose by the time the confirm step actually
// shows, so the confirm prompt itself must name how many mounts are
// currently placeholders.
func TestRedirectMissingBindMountsShowsConfirmReminder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	source := pasteSourceProvider()
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	ctr := source.containers["src-1"]
	ctr.Mounts = append(ctr.Mounts, domain.Mount{Type: "bind", Source: missing, Destination: "/config-extra", ReadWrite: true})
	source.containers["src-1"] = ctr
	source.snapshot = domain.BuildSnapshot(source.host, []domain.Container{ctr}, time.Unix(1, 0))

	dest := pasteDestProvider()
	model := modelWithSourceSelected(t, source)
	model = yankAndSwitch(t, model, dest)
	model = openPasteReview(t, model)

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	model = updated.(Model)
	msg := runCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter}) // review/fix -> opens the create form
	model = updated.(Model)
	model.createDraft.Confirming = true
	model.width, model.height = 100, 40

	view := ansi.Strip(model.View())
	if !strings.Contains(view, "1 bind mount(s) are placeholders") {
		t.Fatalf("confirm view missing the placeholder reminder:\n%s", view)
	}
}

func TestRedirectMissingBindMountsOverSSHResolvesHomeAndCreatesDirectory(t *testing.T) {
	fake := withFakeSSHRun(t)
	fake.respond("echo $HOME", "/home/allie\n", nil)

	source := pasteSourceProvider()
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	fake.respond("test -e '"+missing+"'", "", errors.New("no such file or directory"))
	ctr := source.containers["src-1"]
	ctr.Mounts = append(ctr.Mounts, domain.Mount{Type: "bind", Source: missing, Destination: "/config-extra", ReadWrite: true})
	source.containers["src-1"] = ctr
	source.snapshot = domain.BuildSnapshot(source.host, []domain.Container{ctr}, time.Unix(1, 0))

	dest := pasteDestProvider()
	model := modelWithSourceSelected(t, source)
	model = yankAndSwitch(t, model, dest)
	model.systems = []config.System{{ID: "jarvis", Name: "jarvis", Kind: "ssh", SSHHost: "jarvis"}}
	model.activeSystem = "jarvis"
	model = openPasteReview(t, model)

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	model = updated.(Model)
	msg := runCmd(t, cmd)
	updated, _ = model.Update(msg)
	model = updated.(Model)

	if model.statusErr {
		t.Fatalf("redirect failed: %s", model.status)
	}
	wantDir := "/home/allie/.local/share/whatthedock/paste-placeholders/radarr/config-extra"
	found := false
	for _, call := range fake.calls {
		if call == "mkdir -p "+"'"+wantDir+"'" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ssh calls = %#v, want a quoted mkdir -p for %q", fake.calls, wantDir)
	}
	if got := model.pastePlan.Spec.Labels[clipboard.BindRedirectLabelPrefix+"/config-extra"]; got != missing {
		t.Fatalf("label = %q, want the original path %q", got, missing)
	}
}
