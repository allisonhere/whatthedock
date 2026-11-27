package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/whatthedock/internal/actions"
	"github.com/allisonhere/whatthedock/internal/domain"
)

func openImageCurator(t *testing.T, images ...domain.Image) Model {
	t.Helper()
	model := testModel()
	model.width, model.height = 120, 34
	fake := model.provider.(*fakeProvider)
	for i := range images {
		images[i].ID.Host = fake.host.ID
	}
	fake.images = images

	updated, cmd := model.executeCommand(actions.CurateImages)
	model = updated.(Model)
	updated, _ = model.Update(runCmd(t, cmd).(imageListMsg))
	return updated.(Model)
}

// Enter on an image opens a container-create form prefilled from it — the
// curator used to be delete-only, with no way to run what it lists.
func TestImageCuratorEnterCreatesAContainerFromTheImage(t *testing.T) {
	model := openImageCurator(t, domain.Image{
		ID: domain.ResourceID{ID: "sha256:aaa"}, RepoTags: []string{"codeberg.org/forgejo/forgejo:17.0.2"}, Size: 294,
	})

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(Model)
	if next.overlay != overlayCreate {
		t.Fatalf("overlay = %v after enter, want the create form", next.overlay)
	}
	draft := next.createDraft
	if draft.Mode != createModeStandalone {
		t.Fatalf("draft mode = %v, want standalone (a plain docker container)", draft.Mode)
	}
	if draft.Image != "codeberg.org/forgejo/forgejo:17.0.2" {
		t.Fatalf("draft image = %q, want the image's tag", draft.Image)
	}
	if draft.ContainerName != "forgejo" {
		t.Fatalf("draft name = %q, want a name suggested from the image", draft.ContainerName)
	}
}

func TestImageCuratorEnterOnAnUntaggedImageSaysWhy(t *testing.T) {
	model := openImageCurator(t, domain.Image{ID: domain.ResourceID{ID: "sha256:bbb"}, Dangling: true, Size: 5})

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(Model)
	if next.overlay != overlayImageCuration {
		t.Fatalf("overlay = %v, want the curator to stay open", next.overlay)
	}
	if !strings.Contains(next.imageError, "no tag") {
		t.Fatalf("imageError = %q, want it to say the image has no tag", next.imageError)
	}
}

func TestContainerNameForImage(t *testing.T) {
	cases := map[string]string{
		"codeberg.org/forgejo/forgejo:17.0.2":   "forgejo",
		"nginx":                                 "nginx",
		"lscr.io/linuxserver/radarr:latest":     "radarr",
		"registry.local:5000/team/app@sha256:1": "app",
		"ghcr.io/x/-:1":                         "new-container",
	}
	for ref, want := range cases {
		if got := containerNameForImage(ref); got != want {
			t.Errorf("containerNameForImage(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestImageCuratorHintAdvertisesRun(t *testing.T) {
	model := openImageCurator(t, domain.Image{ID: domain.ResourceID{ID: "sha256:ccc"}, RepoTags: []string{"a:1"}})
	if !strings.Contains(ansi.Strip(model.View()), "enter run") {
		t.Fatal("image curator's command strip doesn't mention enter run")
	}
}
