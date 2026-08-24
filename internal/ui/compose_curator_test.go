package ui

import (
	"context"
	"io"
	"net/http"
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

func TestComposeCuratorSavesRunningStackWithNote(t *testing.T) {
	model, composePath := composeCuratorModel(t)
	updated, cmd := model.executeCommand(actions.CurateCompose)
	model = updated.(Model)
	if cmd != nil || model.overlay != overlayComposeCuration {
		t.Fatalf("overlay/cmd = %v/%v, want compose curator and no async cmd", model.overlay, cmd)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "compose curator") || !strings.Contains(view, "media") {
		t.Fatalf("compose curator missing running stack:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = updated.(Model)
	for _, r := range "keep around" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(Model)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	entries, err := catalog.Load(model.catalogDir)
	if err != nil {
		t.Fatalf("catalog.Load() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "media" || entries[0].SourcePaths[0] != composePath {
		t.Fatalf("entries = %#v, want saved media entry for %s", entries, composePath)
	}
	content, err := catalog.Read(model.catalogDir, entries[0].ID)
	if err != nil {
		t.Fatalf("catalog.Read() error = %v", err)
	}
	if !strings.Contains(content, catalog.NoteHeaderStart) || !strings.Contains(content, "# keep around") || !strings.Contains(content, "jellyfin/jellyfin") {
		t.Fatalf("saved content missing note/source:\n%s", content)
	}
}

func TestComposeCuratorCatalogEnterOpensPreviewAndCLoadsCreate(t *testing.T) {
	model, _ := composeCuratorModel(t)
	entry, err := catalog.SaveStack(model.catalogDir, "dash", "", "local", []string{"/srv/dash/compose.yml"}, []catalog.FileContent{{
		Name: "compose.yml", Content: "services:\n  dash:\n    image: ghcr.io/allisonhere/dash:latest\n", Primary: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	model.reloadComposeCuration()
	model.overlay = overlayComposeCuration
	model.composeCuratorTab = composeCuratorCatalog
	if view := ansi.Strip(model.View()); !strings.Contains(view, "enter preview") || !strings.Contains(view, "c create") {
		t.Fatalf("catalog command strip missing preview/create actions:\n%s", view)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.overlay != overlayComposeCuration || model.composeCuratorMode != composeCuratorPreview {
		t.Fatalf("overlay/mode = %v/%v, want compose preview", model.overlay, model.composeCuratorMode)
	}
	if view := ansi.Strip(model.View()); !strings.Contains(view, "Preview") || !strings.Contains(view, "ghcr.io/allisonhere/dash") {
		t.Fatalf("preview missing metadata/content:\n%s", view)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	model = updated.(Model)
	if model.overlay != overlayCreate {
		t.Fatalf("overlay = %v, want create overlay", model.overlay)
	}
	if model.createDraft.Project != entry.Name || model.createDraft.Service != "dash" || !strings.Contains(model.createDraft.OverrideRaw, "ghcr.io/allisonhere/dash") {
		t.Fatalf("draft = %+v, want loaded catalog compose", model.createDraft)
	}
}

func TestComposeCuratorCatalogCLoadsCreateFromList(t *testing.T) {
	model, _ := composeCuratorModel(t)
	entry, err := catalog.SaveStack(model.catalogDir, "dash", "", "local", []string{"/srv/dash/compose.yml"}, []catalog.FileContent{{
		Name: "compose.yml", Content: "services:\n  dash:\n    image: ghcr.io/allisonhere/dash:latest\n", Primary: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	model.reloadComposeCuration()
	model.overlay = overlayComposeCuration
	model.composeCuratorTab = composeCuratorCatalog

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	model = updated.(Model)
	if model.overlay != overlayCreate || model.createDraft.Project != entry.Name || model.createDraft.Service != "dash" {
		t.Fatalf("overlay/draft = %v/%+v, want Create loaded from catalog", model.overlay, model.createDraft)
	}
}

func TestComposeCuratorPreviewActions(t *testing.T) {
	model, _ := composeCuratorModel(t)
	entry, err := catalog.SaveStack(model.catalogDir, "media", "", "local", nil, []catalog.FileContent{{
		Name: "compose.yml", Content: "services:\n  app:\n    image: nginx\n", Primary: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	model.reloadComposeCuration()
	model.overlay = overlayComposeCuration
	model.composeCuratorTab = composeCuratorCatalog
	model.composeCuratorMode = composeCuratorPreview

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	edited := updated.(Model)
	if edited.composeCuratorMode != composeCuratorEditFile || edited.composeCuratorEditEntryID != entry.ID {
		t.Fatalf("after e mode/id = %v/%q, want edit %s", edited.composeCuratorMode, edited.composeCuratorEditEntryID, entry.ID)
	}

	model.composeCuratorMode = composeCuratorPreview
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	makeLive := updated.(Model)
	if makeLive.composeCuratorMode != composeCuratorDeploy || makeLive.composeDeployPath == "" {
		t.Fatalf("after M mode/path = %v/%q, want make-live path prompt", makeLive.composeCuratorMode, makeLive.composeDeployPath)
	}

	model.composeCuratorMode = composeCuratorPreview
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	back := updated.(Model)
	if back.composeCuratorMode != composeCuratorList {
		t.Fatalf("after esc mode = %v, want list", back.composeCuratorMode)
	}
}

func TestComposeCuratorAddsCatalogEntryFromURL(t *testing.T) {
	model, _ := composeCuratorModel(t)
	original := composeHTTPClient
	composeHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader("services:\n  web:\n    image: nginx\n")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	t.Cleanup(func() { composeHTTPClient = original })
	sourceURL := "https://example.test/compose.yml"
	model.reloadComposeCuration()
	model.overlay = overlayComposeCuration
	model.composeCuratorTab = composeCuratorCatalog

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	model = updated.(Model)
	for _, r := range sourceURL {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(Model)
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("URL import returned nil cmd, want async import")
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	if model.composeCuratorMode != composeCuratorEditFile || !strings.Contains(model.composeCuratorEditor.Value(), "image: nginx") {
		t.Fatalf("mode/editor = %v/%q, want imported draft open in editor", model.composeCuratorMode, model.composeCuratorEditor.Value())
	}
	entries, err := catalog.Load(model.catalogDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Status != catalog.StatusDraft || entries[0].SourcePaths[0] != sourceURL {
		t.Fatalf("entries = %#v, want one draft sourced from URL", entries)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestComposeCuratorBrowseAddsCatalogEntry(t *testing.T) {
	model, _ := composeCuratorModel(t)
	dir := t.TempDir()
	composePath := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(composePath, []byte("services:\n  web:\n    image: caddy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model.overlay = overlayComposeCuration
	model.composeCuratorTab = composeCuratorCatalog
	model.composeCuratorMode = composeCuratorBrowse
	if cmd := model.browseComposeCatalogDir(dir); cmd != nil {
		updated, _ := model.Update(cmd())
		model = updated.(Model)
	}
	if len(model.composeBrowseFiles) == 0 {
		t.Fatal("browse files empty, want compose file")
	}
	for i, entry := range model.composeBrowseFiles {
		if entry.Path == composePath {
			model.composeBrowseCursor = i
			break
		}
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("browse selection returned nil cmd, want async import")
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	if model.composeCuratorMode != composeCuratorEditFile || !strings.Contains(model.composeCuratorEditor.Value(), "image: caddy") {
		t.Fatalf("mode/editor = %v/%q, want browsed file open in editor", model.composeCuratorMode, model.composeCuratorEditor.Value())
	}
}

func TestComposeCuratorNewBlankDraftOpensEditor(t *testing.T) {
	model, _ := composeCuratorModel(t)
	model.reloadComposeCuration()
	model.overlay = overlayComposeCuration
	model.composeCuratorTab = composeCuratorCatalog

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	model = updated.(Model)
	if model.composeCuratorMode != composeCuratorEditFile || !strings.Contains(model.composeCuratorEditor.Value(), "services:") {
		t.Fatalf("mode/editor = %v/%q, want blank draft editor", model.composeCuratorMode, model.composeCuratorEditor.Value())
	}
	entries, err := catalog.Load(model.catalogDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Status != catalog.StatusDraft {
		t.Fatalf("entries = %#v, want one draft", entries)
	}
}

func TestComposeCuratorRunningStackOpensLibraryEditor(t *testing.T) {
	model, composePath := composeCuratorModel(t)
	updated, _ := model.executeCommand(actions.CurateCompose)
	model = updated.(Model)

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("opening uncataloged running stack returned nil cmd, want async catalog capture")
	}
	if !strings.Contains(model.composeCuratorMessage, "opening media") || model.composeCuratorErr != "" {
		t.Fatalf("message/error after key = %q/%q, want immediate opening feedback", model.composeCuratorMessage, model.composeCuratorErr)
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	if model.overlay != overlayComposeCuration || model.composeCuratorTab != composeCuratorCatalog || model.composeCuratorMode != composeCuratorEditFile {
		t.Fatalf("overlay/tab/mode = %v/%v/%v, want compose catalog editor", model.overlay, model.composeCuratorTab, model.composeCuratorMode)
	}
	if !strings.Contains(model.composeCuratorEditor.Value(), "jellyfin/jellyfin") {
		t.Fatalf("editor value = %q, want live compose content", model.composeCuratorEditor.Value())
	}
	entries, err := catalog.Load(model.catalogDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].SourcePaths[0] != composePath || entries[0].Status != catalog.StatusSaved {
		t.Fatalf("entries = %#v, want one saved library entry for live stack", entries)
	}
	data, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "jellyfin/jellyfin") {
		t.Fatalf("live compose file changed unexpectedly: %q", string(data))
	}
}

func TestComposeCuratorRunningStackOpensExistingLibraryEntry(t *testing.T) {
	model, composePath := composeCuratorModel(t)
	entry, err := catalog.SaveStack(model.catalogDir, "media", "", "local", []string{composePath}, []catalog.FileContent{{
		Name: "compose.yml", SourcePath: composePath, Content: "services:\n  app:\n    image: nginx\n", Primary: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	model.reloadComposeCuration()
	model.overlay = overlayComposeCuration
	model.composeCuratorTab = composeCuratorRunning

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.composeCuratorMode != composeCuratorEditFile || model.composeCuratorEditEntryID != entry.ID {
		t.Fatalf("mode/edit id = %v/%q, want existing catalog editor for %s", model.composeCuratorMode, model.composeCuratorEditEntryID, entry.ID)
	}
	entries, err := catalog.Load(model.catalogDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %#v, want no duplicate when opening existing entry", entries)
	}
}

func TestComposeCuratorArchiveAndDeleteCatalogEntry(t *testing.T) {
	model, _ := composeCuratorModel(t)
	entry, err := catalog.SaveStack(model.catalogDir, "dash", "", "local", nil, []catalog.FileContent{{
		Name: "compose.yml", Content: "services:\n  dash:\n    image: nginx\n", Primary: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	model.reloadComposeCuration()
	model.overlay = overlayComposeCuration
	model.composeCuratorTab = composeCuratorCatalog

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = updated.(Model)
	entries, err := catalog.Load(model.catalogDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].Archived {
		t.Fatalf("entries = %#v, want archived", entries)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	model = updated.(Model)
	if model.composeCuratorMode != composeCuratorDelete {
		t.Fatalf("mode = %v, want delete confirmation", model.composeCuratorMode)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	entries, err = catalog.Load(model.catalogDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries after deleting %s = %#v, want empty", entry.ID, entries)
	}
}

func TestComposeCuratorDeployWritesFilesAndRunsCompose(t *testing.T) {
	model, _ := composeCuratorModel(t)
	entry, err := catalog.SaveStack(model.catalogDir, "media", "", "local", nil, []catalog.FileContent{
		{Name: "compose.yml", Content: "services:\n  app:\n    image: nginx\n", Primary: true},
		{Name: "override.yml", Content: "services:\n  app:\n    ports:\n      - 8080:80\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	model.reloadComposeCuration()
	model.composeCuratorTab = composeCuratorCatalog
	target := filepath.Join(t.TempDir(), "deploy")
	var gotProject string
	var gotFiles []string
	var gotArgs []string
	original := composeFilesCommand
	defer func() { composeFilesCommand = original }()
	composeFilesCommand = func(_ context.Context, _ config.System, project string, files []string, args ...string) error {
		gotProject = project
		gotFiles = append([]string(nil), files...)
		gotArgs = append([]string(nil), args...)
		return nil
	}

	if err := model.deployComposeCatalogEntry(entry, target); err != nil {
		t.Fatalf("deployComposeCatalogEntry() error = %v", err)
	}
	if gotProject != "media" || strings.Join(gotArgs, " ") != "up -d" {
		t.Fatalf("compose command project/args = %q/%#v, want media/up -d", gotProject, gotArgs)
	}
	if len(gotFiles) != 2 || filepath.Base(gotFiles[0]) != "compose.yml" || filepath.Base(gotFiles[1]) != "override.yml" {
		t.Fatalf("compose files = %#v, want compose.yml and override.yml", gotFiles)
	}
	for _, file := range gotFiles {
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("deployed file %s missing: %v", file, err)
		}
	}
}

func TestComposeCuratorEditsCatalogEntryWithRipple(t *testing.T) {
	model, _ := composeCuratorModel(t)
	entry, err := catalog.SaveStack(model.catalogDir, "media", "keep note", "local", nil, []catalog.FileContent{{
		Name: "compose.yml", Content: "services:\n  app:\n    image: nginx\n", Primary: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	model.reloadComposeCuration()
	model.overlay = overlayComposeCuration
	model.composeCuratorTab = composeCuratorCatalog

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	model = updated.(Model)
	if model.composeCuratorMode != composeCuratorEditFile {
		t.Fatalf("mode = %v, want compose editor", model.composeCuratorMode)
	}
	model.composeCuratorEditor.SetValue("services:\n  app:\n    image: caddy\n")
	updated, _ = model.Update(ripple.SubmitMsg{})
	model = updated.(Model)
	if model.composeCuratorMode != composeCuratorList || model.composeCuratorErr != "" {
		t.Fatalf("after save mode/err = %v/%q, want list/no error", model.composeCuratorMode, model.composeCuratorErr)
	}
	content, err := catalog.Read(model.catalogDir, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "# keep note") || !strings.Contains(content, "image: caddy") {
		t.Fatalf("edited content missing note or image:\n%s", content)
	}
}

func TestComposeCuratorTagsAndStatusFilter(t *testing.T) {
	model, composePath := composeCuratorModel(t)
	active, err := catalog.SaveStack(model.catalogDir, "media", "", "local", []string{composePath}, []catalog.FileContent{{
		Name: "compose.yml", Content: "services:\n  app:\n    image: nginx\n", Primary: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SaveStack(model.catalogDir, "old dash", "", "local", []string{"/missing/compose.yml"}, []catalog.FileContent{{
		Name: "compose.yml", Content: "services:\n  dash:\n    image: nginx\n", Primary: true,
	}}); err != nil {
		t.Fatal(err)
	}
	model.reloadComposeCuration()
	model.overlay = overlayComposeCuration
	model.composeCuratorTab = composeCuratorCatalog

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	model = updated.(Model)
	for _, r := range "Prod, media, #Prod" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(Model)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	entries, err := catalog.Load(model.catalogDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(entries[0].Tags, ","); entries[0].ID != active.ID || got != "media,prod" {
		t.Fatalf("first entry/tags = %s/%q, want %s/media,prod", entries[0].ID, got, active.ID)
	}

	model.composeCuratorFilter = "prod"
	if rows := model.composeCuratorRows(); len(rows) != 1 {
		t.Fatalf("tag-filtered rows = %d, want 1", len(rows))
	}
	model.composeCuratorFilter = ""
	model.composeCuratorStatus = composeStatusUnused
	if rows := model.composeCuratorRows(); len(rows) != 1 || rows[0].(catalog.Entry).Name != "old dash" {
		t.Fatalf("unused rows = %#v, want old dash only", rows)
	}
	model.composeCuratorStatus = composeStatusActive
	if rows := model.composeCuratorRows(); len(rows) != 1 || rows[0].(catalog.Entry).ID != active.ID {
		t.Fatalf("active rows = %#v, want active media only", rows)
	}
}

func TestComposeCuratorDeployConflictRequiresConfirmation(t *testing.T) {
	model, _ := composeCuratorModel(t)
	entry, err := catalog.SaveStack(model.catalogDir, "media", "", "local", nil, []catalog.FileContent{{
		Name: "compose.yml", Content: "services:\n  app:\n    image: nginx\n", Primary: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	targetDir := t.TempDir()
	target := filepath.Join(targetDir, "compose.yml")
	if err := os.WriteFile(target, []byte("services:\n  old:\n    image: busybox\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	original := composeFilesCommand
	defer func() { composeFilesCommand = original }()
	composeFilesCommand = func(_ context.Context, _ config.System, _ string, _ []string, _ ...string) error {
		calls++
		return nil
	}

	var prepareErr error
	model, prepareErr = model.prepareComposeDeploy(entry, target)
	if prepareErr != nil {
		t.Fatalf("prepareComposeDeploy() error = %v", prepareErr)
	}
	if model.composeCuratorMode != composeCuratorConflict || len(model.composeDeployConflicts) != 1 || calls != 0 {
		t.Fatalf("mode/conflicts/calls = %v/%#v/%d, want conflict/one/no compose", model.composeCuratorMode, model.composeDeployConflicts, calls)
	}
	model.overlay = overlayComposeCuration
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if model.composeCuratorMode != composeCuratorList || calls != 1 {
		t.Fatalf("after confirm mode/calls = %v/%d, want list/1", model.composeCuratorMode, calls)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "image: nginx") {
		t.Fatalf("target content after deploy = %q, want catalog compose", string(data))
	}
	entries, err := catalog.Load(model.catalogDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Status != catalog.StatusApplied {
		t.Fatalf("entries = %#v, want applied after make live", entries)
	}
}

func TestComposeCuratorSaveAsDraftDuplicatesCatalogEntry(t *testing.T) {
	model, _ := composeCuratorModel(t)
	entry, err := catalog.SaveStack(model.catalogDir, "media", "keep note", "local", nil, []catalog.FileContent{{
		Name: "compose.yml", Content: "services:\n  app:\n    image: nginx\n", Primary: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.SetTags(model.catalogDir, entry.ID, []string{"prod"}); err != nil {
		t.Fatal(err)
	}
	model.reloadComposeCuration()
	model.overlay = overlayComposeCuration
	model.composeCuratorTab = composeCuratorCatalog

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	model = updated.(Model)
	entries, err := catalog.Load(model.catalogDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %#v, want original plus draft", entries)
	}
	var draft catalog.Entry
	for _, candidate := range entries {
		if candidate.Status == catalog.StatusDraft {
			draft = candidate
		}
	}
	if draft.ID == "" || draft.Note != "keep note" || strings.Join(draft.Tags, ",") != "prod" {
		t.Fatalf("draft = %#v, want copied draft metadata", draft)
	}
	model.reloadComposeCuration()
	model.composeCuratorStatus = composeStatusDraft
	if rows := model.composeCuratorRows(); len(rows) != 1 || rows[0].(catalog.Entry).ID != draft.ID {
		t.Fatalf("draft rows = %#v, want only saved draft", rows)
	}
}

func composeCuratorModel(t *testing.T) (Model, string) {
	t.Helper()
	dir := t.TempDir()
	composePath := filepath.Join(dir, "compose.yml")
	content := "services:\n  jellyfin:\n    image: jellyfin/jellyfin\n"
	if err := os.WriteFile(composePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := newFakeProvider()
	ctr := domain.Container{
		ID:     domain.ResourceID{Host: provider.host.ID, ID: "compose-1"},
		Name:   "media-jellyfin-1",
		Image:  "jellyfin/jellyfin",
		State:  domain.StateRunning,
		Labels: map[string]string{},
		Compose: domain.ComposeRef{
			Project:     "media",
			Service:     "jellyfin",
			ConfigFiles: composePath,
		},
	}
	provider.containers = map[string]domain.Container{ctr.ID.ID: ctr}
	provider.snapshot = domain.BuildSnapshot(provider.host, []domain.Container{ctr}, time.Unix(2, 0))
	model := NewModel(provider)
	model.snapshot = provider.snapshot
	model.rows = model.buildRows()
	model.catalogDir = filepath.Join(dir, "catalog")
	model.width, model.height = 120, 34
	return model, composePath
}
