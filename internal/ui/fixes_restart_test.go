package ui

import (
	"testing"

	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/clipboard"
)

func pasteDraftWithRetries() createDraft {
	plan := clipboard.PastePlan{Spec: app.ContainerCreateSpec{
		Name: "worker", Image: "img", RestartPolicy: "on-failure", RestartMaxRetries: 5,
	}}
	draft := draftFromPastePlan(plan)
	draft.baseline = draft.editableValues() // what openCreateOverlayWithDraft records
	return draft
}

// The restart field can't show a retry cap, so a paste that leaves the policy
// alone must keep the plan's cap; changing the policy deliberately drops it.
func TestPasteKeepsRetryCapUnlessTheRestartPolicyChanges(t *testing.T) {
	draft := pasteDraftWithRetries()
	spec, err := draft.ContainerSpec()
	if err != nil {
		t.Fatal(err)
	}
	if spec.RestartPolicy != "on-failure" || spec.RestartMaxRetries != 5 {
		t.Fatalf("untouched paste restart = %q:%d, want on-failure:5", spec.RestartPolicy, spec.RestartMaxRetries)
	}

	draft.Restart = "always"
	spec, err = draft.ContainerSpec()
	if err != nil {
		t.Fatal(err)
	}
	if spec.RestartPolicy != "always" || spec.RestartMaxRetries != 0 {
		t.Fatalf("changed paste restart = %q:%d, want always:0", spec.RestartPolicy, spec.RestartMaxRetries)
	}
}

func TestCloneKeepsRetryCap(t *testing.T) {
	model := modelSelecting("", "", "")
	model.selected.Image = "img"
	model.selected.RestartPolicy = "on-failure"
	model.selected.RestartMaxRetries = 3

	draft := model.defaultCloneDraft()
	draft.baseline = draft.editableValues()
	spec, err := draft.ContainerSpec()
	if err != nil {
		t.Fatal(err)
	}
	if spec.RestartPolicy != "on-failure" || spec.RestartMaxRetries != 3 {
		t.Fatalf("clone restart = %q:%d, want on-failure:3", spec.RestartPolicy, spec.RestartMaxRetries)
	}
}

// The Mounts field is "source:target" text, so re-parsing it forgets whether a
// mount was a volume or a bind. A volume whose source looks like a path would
// then be guessed to be a bind mount.
func TestPasteKeepsMountTypeForUnchangedMounts(t *testing.T) {
	plan := clipboard.PastePlan{Spec: app.ContainerCreateSpec{
		Name: "w", Image: "img",
		Mounts: []app.MountBinding{{Type: "volume", Source: "/mnt/pool/data", Destination: "/data"}},
	}}
	draft := draftFromPastePlan(plan)
	draft.baseline = draft.editableValues()

	// Unchanged text: the mount list is rebuilt from the form, Type intact.
	draft.baseline.Mounts = "" // force the re-parse path
	spec, err := draft.ContainerSpec()
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Mounts) != 1 || spec.Mounts[0].Type != "volume" {
		t.Fatalf("mounts = %#v, want the volume type kept", spec.Mounts)
	}

	// A mount whose source the user rewrote has no recorded type to keep.
	draft.Mounts = "/other/path:/data"
	spec, err = draft.ContainerSpec()
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Mounts) != 1 || spec.Mounts[0].Type != "" {
		t.Fatalf("mounts = %#v, want the type left empty for inference after the source changed", spec.Mounts)
	}
}
