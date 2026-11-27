package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allisonhere/whatthedock/internal/config"
)

const longFormBase = `services:
  dash:
    image: img:1
    ports:
      - "8080:80"
      - target: 9000
      - target: 9001
        published: 9001
        protocol: udp
    volumes:
      - ./data:/config
      - type: volume
        target: /cache
      - type: bind
        source: ./x
        target: /x
        bind:
          propagation: shared
`

// The form only loads entries it can write as one short string. Editing one
// port or mount must not delete the container-only port, the anonymous
// volume, or the extras on a long-form entry the user didn't touch.
func TestMergeKeepsEntriesTheFormCannotRepresent(t *testing.T) {
	fields := composeOverrideService{
		Ports:   []string{"8080:81", "9001:9001/udp"},
		Volumes: []string{"./data:/config2", "./x:/x"},
	}
	out, err := mergeComposeServiceFields([]byte(longFormBase), "dash", fields, composeFieldPorts|composeFieldVolumes)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	for _, want := range []string{
		"8080:81",             // the edited port
		"./data:/config2",     // the edited mount
		"target: 9000",        // container-only port: not representable, kept
		"target: /cache",      // anonymous volume: not representable, kept
		"protocol: udp",       // untouched long-form port keeps its original node
		"propagation: shared", // untouched long-form volume keeps its extras
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("merged service lost %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"8080:80", "./data:/config\n"} {
		if strings.Contains(got, gone) {
			t.Fatalf("merged service still has the replaced entry %q:\n%s", gone, got)
		}
	}
}

// An entry the user removed from the form really is removed; only the ones
// the form never showed are left alone.
func TestMergeRemovesDeletedRepresentableEntries(t *testing.T) {
	fields := composeOverrideService{Ports: []string{"9001:9001/udp"}, Volumes: []string{"./data:/config"}}
	out, err := mergeComposeServiceFields([]byte(longFormBase), "dash", fields, composeFieldPorts|composeFieldVolumes)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "8080:80") {
		t.Fatalf("removed port 8080:80 is still in the file:\n%s", got)
	}
	if strings.Contains(got, "source: ./x") {
		t.Fatalf("removed bind volume is still in the file:\n%s", got)
	}
	if !strings.Contains(got, "target: 9000") || !strings.Contains(got, "target: /cache") {
		t.Fatalf("entries the form can't show were deleted along with the removed ones:\n%s", got)
	}
}

// The same through the real Edit flow: change one port and one mount, apply,
// and read the file back.
func TestComposeEditFlowKeepsContainerOnlyPortsAndAnonymousVolumes(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	composeCommand = func(context.Context, composeCreateSpec, ...string) error { return nil }

	base := filepath.Join(t.TempDir(), "compose.yaml")
	if err := os.WriteFile(base, []byte(longFormBase), 0o644); err != nil {
		t.Fatal(err)
	}
	model := modelSelecting("dash", "dash", base)
	loadCmd := model.openEditOverlay()
	updated, _ := model.Update(runCmd(t, loadCmd))
	model = updated.(Model)
	model.createDraft.Ports = "8080:81, 9001:9001/udp"
	model.createDraft.Mounts = "./data:/config2, ./x:/x"

	_, done := applyComposeThroughModel(t, model)
	if done.err != nil {
		t.Fatal(done.err)
	}
	data, _ := os.ReadFile(base)
	got := string(data)
	for _, want := range []string{"8080:81", "target: 9000", "target: /cache", "propagation: shared"} {
		if !strings.Contains(got, want) {
			t.Fatalf("file after the edit flow lost %q:\n%s", want, got)
		}
	}
}

// A service defined in the base and also in a leftover override is the two
// layered. Editing it (the form comes from the override) must fold the
// override into the base, not delete it and revert its settings.
func TestComposeEditFlowFoldsLeftoverOverrideIntoBase(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	composeCommand = func(context.Context, composeCreateSpec, ...string) error { return nil }

	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yaml")
	override := filepath.Join(dir, "compose.whatthedock.dash.yml")
	if err := os.WriteFile(base, []byte(`services:
  dash:
    image: img:1
    restart: unless-stopped
    ports:
      - "8080:80"
    environment:
      - KEEP=base
      - SHARED=base
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(override, []byte(`services:
  dash:
    restart: always
    ports:
      - "7000:7000"
    environment:
      - SHARED=override
      - FOO=bar
    labels:
      owner: me
`), 0o600); err != nil {
		t.Fatal(err)
	}

	model := modelSelecting("dash", "dash", base)
	if loadCmd := model.openEditOverlay(); loadCmd != nil {
		updated, _ := model.Update(runCmd(t, loadCmd))
		model = updated.(Model)
	}
	model.createDraft.Image = "img:2"
	_, done := applyComposeThroughModel(t, model)
	if done.err != nil {
		t.Fatal(done.err)
	}

	data, _ := os.ReadFile(base)
	got := string(data)
	for _, want := range []string{
		"image: img:2",    // the edit
		"restart: always", // override-only setting, now in the base
		"8080:80",         // base port kept...
		"7000:7000",       // ...and the override's added
		"KEEP=base",       // base env kept
		"SHARED=override", // override wins a shared variable
		"FOO=bar",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("base after the edit lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "unless-stopped") || strings.Contains(got, "SHARED=base") {
		t.Fatalf("base still has values the override replaced:\n%s", got)
	}

	// The override had a key the base merge can't carry (labels), so it stays,
	// minus everything that moved into the base.
	rest, err := os.ReadFile(override)
	if err != nil {
		t.Fatalf("override was deleted although it holds unmanaged keys: %v", err)
	}
	if !strings.Contains(string(rest), "owner: me") {
		t.Fatalf("override lost its labels:\n%s", rest)
	}
	for _, moved := range []string{"restart", "7000", "FOO", "ports", "environment"} {
		if strings.Contains(string(rest), moved) {
			t.Fatalf("override still holds %q, which now lives in the base (it would be applied twice):\n%s", moved, rest)
		}
	}
}

// With nothing but form-managed fields in the override, folding leaves it
// empty and it is removed, as before.
func TestComposeEditFlowRemovesOverrideOnceFullyFolded(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	composeCommand = func(context.Context, composeCreateSpec, ...string) error { return nil }

	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yaml")
	override := filepath.Join(dir, "compose.whatthedock.dash.yml")
	os.WriteFile(base, []byte("services:\n  dash:\n    image: img:1\n"), 0o644)
	os.WriteFile(override, []byte("services:\n  dash:\n    restart: always\n"), 0o644)

	model := modelSelecting("dash", "dash", base)
	if loadCmd := model.openEditOverlay(); loadCmd != nil {
		updated, _ := model.Update(runCmd(t, loadCmd))
		model = updated.(Model)
	}
	model.createDraft.Image = "img:2"
	if _, done := applyComposeThroughModel(t, model); done.err != nil {
		t.Fatal(done.err)
	}
	if _, err := os.Stat(override); err == nil {
		t.Fatal("a fully folded override should be removed")
	}
	data, _ := os.ReadFile(base)
	if !strings.Contains(string(data), "restart: always") {
		t.Fatalf("base lost the override's restart policy:\n%s", data)
	}
}

// The same fold over SSH: the base gets the override's fields, and an
// override that still holds unmanaged keys is rewritten, not removed.
func TestRemoteMergeFoldsOverrideAndKeepsUnmanagedKeys(t *testing.T) {
	original := composeCommand
	defer func() { composeCommand = original }()
	composeCommand = func(context.Context, composeCreateSpec, ...string) error { return nil }
	fake := withFakeSSHRun(t)

	const base, override = "/srv/dash/compose.yaml", "/srv/dash/compose.whatthedock.dash.yml"
	fake.respond("cat '"+override+"'", "services:\n  dash:\n    restart: always\n    labels:\n      owner: me\n", nil)

	spec := composeCreateSpec{
		Project: "dash", Service: "dash", BaseFile: base, OverrideFile: override,
		Content:      "services:\n  dash:\n    image: img:2\n    restart: always\n",
		FoldOverride: true,
		System:       config.System{Kind: "ssh", SSHHost: "h", Name: "h"},
	}
	baseContent := []byte("services:\n  dash:\n    image: img:1\n    restart: unless-stopped\n")
	if err := mergeComposeCreateIntoBaseRemote(context.Background(), spec, baseContent); err != nil {
		t.Fatal(err)
	}

	var wroteBase, wroteOverride, removedOverride bool
	for _, call := range fake.calls {
		switch {
		case strings.HasPrefix(call, remoteComposeWriteScript(base+".tmp", base)+"\x00stdin="):
			wroteBase = strings.Contains(call, "image: img:2") && strings.Contains(call, "restart: always")
		case strings.HasPrefix(call, remoteComposeWriteScript(override+".tmp", override)+"\x00stdin="):
			wroteOverride = strings.Contains(call, "owner: me") && !strings.Contains(call, "restart")
		case call == "rm -f '"+override+"'":
			removedOverride = true
		}
	}
	if !wroteBase || !wroteOverride || removedOverride {
		t.Fatalf("wroteBase=%v wroteResidualOverride=%v removedOverride=%v; calls = %#v", wroteBase, wroteOverride, removedOverride, fake.calls)
	}
}
