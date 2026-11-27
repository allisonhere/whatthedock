package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	whatthedock "github.com/allisonhere/whatthedock"
)

// The embedded icons are what gets installed, byte for byte, and are real PNGs.
func TestInstallDesktopWritesTheEmbeddedIcons(t *testing.T) {
	data := t.TempDir()
	var out, errOut bytes.Buffer
	if code := runInstallDesktop(&out, &errOut, "/opt/whatthedock", data); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for size, want := range map[string][]byte{"256x256": whatthedock.Icon256, "512x512": whatthedock.Icon512} {
		got, err := os.ReadFile(filepath.Join(data, "icons", "hicolor", size, "apps", "whatthedock.png"))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s icon differs from the embedded one (%v)", size, err)
		}
		if !bytes.HasPrefix(got, []byte("\x89PNG\r\n\x1a\n")) {
			t.Fatalf("%s icon is not a PNG", size)
		}
	}
	if _, err := os.Stat(filepath.Join(data, "applications", "whatthedock.desktop")); err != nil {
		t.Fatalf("no launcher written: %v", err)
	}
}
