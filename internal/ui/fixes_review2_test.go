package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/whatthedock/internal/actions"
	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/domain"
)

func loadEditedDraft(t *testing.T, base string) Model {
	t.Helper()
	model := modelSelecting("dash", "dash", base)
	if cmd := model.openEditOverlay(); cmd != nil {
		updated, _ := model.Update(runCmd(t, cmd))
		model = updated.(Model)
	}
	return model
}

// Confirming an edit with nothing changed must not write the copy of the file
// read when the form opened over what is on disk now.
func TestUnchangedEditConfirmKeepsAConcurrentEditToTheBase(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	var calls []string
	composeCommand = func(_ context.Context, _ composeCreateSpec, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	base := filepath.Join(t.TempDir(), "compose.yaml")
	os.WriteFile(base, []byte("services:\n  dash:\n    image: img:1\n"), 0o644)
	model := loadEditedDraft(t, base)

	fresh := "services:\n  dash:\n    image: img:2\n    restart: always\n"
	os.WriteFile(base, []byte(fresh), 0o644)
	if _, done := applyComposeThroughModel(t, model); done.err != nil {
		t.Fatal(done.err)
	}
	data, _ := os.ReadFile(base)
	if string(data) != fresh {
		t.Fatalf("base was overwritten with the stale copy:\n%s", data)
	}
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "up") {
		t.Fatalf("compose calls = %q, want just an up", calls)
	}
}

// YAML the user hand-edited in the editor, and a catalog template, are not
// stale copies: they are what the user asked to write.
func TestHandEditedAndCatalogDraftsStillWriteTheBaseWholesale(t *testing.T) {
	base := filepath.Join(t.TempDir(), "compose.yaml")
	os.WriteFile(base, []byte("services:\n  dash:\n    image: img:1\n"), 0o644)
	draft := loadEditedDraft(t, base).createDraft

	spec, err := draft.ComposeSpec(config.DefaultSystem())
	if err != nil {
		t.Fatal(err)
	}
	if !spec.Unchanged || spec.FullBase {
		t.Fatalf("as loaded: Unchanged=%v FullBase=%v, want true/false", spec.Unchanged, spec.FullBase)
	}

	draft.OverrideLoaded = false // what saveCreateEditor does
	if spec, _ = draft.ComposeSpec(config.DefaultSystem()); spec.Unchanged || !spec.FullBase {
		t.Fatalf("hand-edited: Unchanged=%v FullBase=%v, want false/true", spec.Unchanged, spec.FullBase)
	}

	draft.OverrideLoaded, draft.catalogLoaded = true, true
	if spec, _ = draft.ComposeSpec(config.DefaultSystem()); spec.Unchanged || !spec.FullBase {
		t.Fatalf("catalog: Unchanged=%v FullBase=%v, want false/true", spec.Unchanged, spec.FullBase)
	}
}

// "80", "80/udp", "127.0.0.1::80" and an anonymous "/data" volume can't go in
// the form's fields. Loading them used to pass Validate and then fail when
// applying; now they stay out of the form and in the file.
func TestContainerOnlyShortEntriesStayOutOfTheFormButSurviveAnEdit(t *testing.T) {
	ports := normalizeComposePorts([]interface{}{"80", "80/udp", "127.0.0.1::80", "8080:80"})
	if len(ports) != 1 || ports[0] != "8080:80" {
		t.Fatalf("ports = %q, want only 8080:80", ports)
	}
	vols := normalizeComposeVolumes([]interface{}{"/data", "./a:/b"})
	if len(vols) != 1 || vols[0] != "./a:/b" {
		t.Fatalf("volumes = %q, want only ./a:/b", vols)
	}

	const src = "services:\n  dash:\n    image: img:1\n    ports:\n      - \"80\"\n      - 80/udp\n      - 8080:80\n    volumes:\n      - /data\n      - ./a:/b\n"
	out, err := mergeComposeServiceFields([]byte(src), "dash",
		composeOverrideService{Ports: []string{"9090:80"}, Volumes: []string{"./a:/c"}}, composeFieldPorts|composeFieldVolumes)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{`"80"`, "80/udp", "9090:80", "/data", "./a:/c"} {
		if !strings.Contains(got, want) {
			t.Fatalf("merge lost %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"8080:80", "./a:/b"} {
		if strings.Contains(got, gone) {
			t.Fatalf("merge kept the replaced %q:\n%s", gone, got)
		}
	}
}

func TestComposeEditWithContainerOnlyPortLoadsAndApplies(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	composeCommand = func(context.Context, composeCreateSpec, ...string) error { return nil }
	base := filepath.Join(t.TempDir(), "compose.yaml")
	os.WriteFile(base, []byte("services:\n  dash:\n    image: img:1\n    ports:\n      - \"80\"\n      - 8080:80\n"), 0o644)

	model := loadEditedDraft(t, base)
	model.createDraft.Ports = "8081:80"
	if _, done := applyComposeThroughModel(t, model); done.err != nil {
		t.Fatalf("applying an edit of a service with a container-only port: %v", done.err)
	}
	data, _ := os.ReadFile(base)
	if !strings.Contains(string(data), `"80"`) || !strings.Contains(string(data), "8081:80") {
		t.Fatalf("base after the edit:\n%s", data)
	}
}

// The SSH override check lands after the user may have started typing.
func TestAdoptBackfillKeepsFieldsTheUserAlreadyEdited(t *testing.T) {
	model := modelSelecting("media", "shelfmark", "/srv/media/compose.yml")
	model.selected.Ports = []domain.Port{{IP: "0.0.0.0", Private: 8788, Public: 8788, Type: "tcp"}}
	model.selected.Env = []string{"PUID=1000"}
	model.openCreateOverlayWithDraft(model.selectionCreateDraft())
	model.createDraft.Editing = true
	model.createDraft.Ports = "1234:80"

	updated, _ := model.Update(createOverrideCheckMsg{service: model.createDraft.Service, base: model.createDraft.ComposeFile, baseFileMissing: true})
	model = updated.(Model)

	if model.createDraft.Ports != "1234:80" {
		t.Fatalf("Ports = %q, the user's edit was overwritten", model.createDraft.Ports)
	}
	if !strings.Contains(model.createDraft.Env, "PUID=1000") {
		t.Fatalf("Env = %q, an untouched field should still be backfilled", model.createDraft.Env)
	}
}

func TestPaletteDeleteAsksAboutScopeLikeTheDKey(t *testing.T) {
	model := testModelWithSelectedContainer() // project "media" has two services
	next, _ := model.executeCommand(actions.Delete)
	if got := next.(Model).overlay; got != overlayDeleteScope {
		t.Fatalf("palette Delete overlay = %v, want overlayDeleteScope", got)
	}
}

func TestCtrlCCancelsHostPowerConfirm(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.overlay = overlayHostPowerConfirm
	model.hostPowerConfirmInput = []rune("ho")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(Model)
	if got.overlay != overlayNone || len(got.hostPowerConfirmInput) != 0 {
		t.Fatalf("after ctrl+c overlay=%v input=%q, want the confirm closed and cleared", got.overlay, string(got.hostPowerConfirmInput))
	}
}
