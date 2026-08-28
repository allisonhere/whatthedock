package clipboard

import "testing"

func TestDeploymentClipboardYankAndCurrent(t *testing.T) {
	c := NewDeploymentClipboard()
	if _, ok := c.Current(); ok {
		t.Fatal("Current() ok = true on an empty clipboard")
	}
	c = c.Yank(PortableContainer{Name: "radarr"})
	current, ok := c.Current()
	if !ok || current.Name != "radarr" {
		t.Fatalf("Current() = %#v/%v, want radarr/true", current, ok)
	}
}

func TestDeploymentClipboardHistoryMostRecentFirst(t *testing.T) {
	c := NewDeploymentClipboard()
	c = c.Yank(PortableContainer{Name: "radarr"})
	c = c.Yank(PortableContainer{Name: "sonarr"})
	history := c.History()
	if len(history) != 2 || history[0].Name != "sonarr" || history[1].Name != "radarr" {
		t.Fatalf("History() = %#v, want [sonarr, radarr]", history)
	}
	if c.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", c.Len())
	}
}

func TestDeploymentClipboardCapsHistory(t *testing.T) {
	c := NewDeploymentClipboard()
	for i := 0; i < defaultHistoryLimit+3; i++ {
		c = c.Yank(PortableContainer{Name: "item"})
	}
	if c.Len() != defaultHistoryLimit {
		t.Fatalf("Len() = %d, want capped at %d", c.Len(), defaultHistoryLimit)
	}
}
