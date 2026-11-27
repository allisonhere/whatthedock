package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testOptions(t *testing.T) Options {
	t.Helper()
	return Options{
		DataHome: t.TempDir(),
		Exe:      "/opt/whatthedock/whatthedock",
		Icons:    map[int][]byte{256: []byte("png256"), 512: []byte("png512")},
	}
}

func TestInstallWritesIconsAndEntry(t *testing.T) {
	opts := testOptions(t)
	result, err := Install(opts)
	if err != nil {
		t.Fatal(err)
	}
	for size, want := range map[string]string{"256x256": "png256", "512x512": "png512"} {
		got, err := os.ReadFile(filepath.Join(opts.DataHome, "icons", "hicolor", size, "apps", "whatthedock.png"))
		if err != nil || string(got) != want {
			t.Fatalf("%s icon = %q, %v; want %q", size, got, err, want)
		}
	}
	entry, err := os.ReadFile(result.DesktopFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[Desktop Entry]", `Exec="/opt/whatthedock/whatthedock"`, "Icon=whatthedock", "Terminal=true"} {
		if !strings.Contains(string(entry), want) {
			t.Fatalf("entry lacks %q:\n%s", want, entry)
		}
	}
	if result.KeptExisting {
		t.Fatal("KeptExisting set on a fresh install")
	}
}

// Running it again (after an upgrade, say) refreshes the icons and our own
// entry, e.g. when the binary moved.
func TestInstallRefreshesItsOwnEntryAndIcons(t *testing.T) {
	opts := testOptions(t)
	if _, err := Install(opts); err != nil {
		t.Fatal(err)
	}
	opts.Exe = "/usr/local/bin/whatthedock"
	opts.Icons = map[int][]byte{256: []byte("new256"), 512: []byte("new512")}
	result, err := Install(opts)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := os.ReadFile(result.DesktopFile)
	if !strings.Contains(string(entry), `"/usr/local/bin/whatthedock"`) {
		t.Fatalf("entry not refreshed:\n%s", entry)
	}
	icon, _ := os.ReadFile(filepath.Join(opts.DataHome, "icons", "hicolor", "256x256", "apps", "whatthedock.png"))
	if string(icon) != "new256" {
		t.Fatalf("icon = %q, want the new one", icon)
	}
}

// A launcher the user wrote (the common case: WhatTheDock.desktop with a
// custom terminal command) must not be replaced, or duplicated.
func TestInstallLeavesAUsersOwnEntryAlone(t *testing.T) {
	opts := testOptions(t)
	apps := filepath.Join(opts.DataHome, "applications")
	os.MkdirAll(apps, 0o755)
	own := "[Desktop Entry]\nName=WhatTheDock\nExec=xdg-terminal-exec --app-id=TUI.tile -e whatthedock\nIcon=whatthedock\n"
	path := filepath.Join(apps, "WhatTheDock.desktop")
	os.WriteFile(path, []byte(own), 0o644)

	result, err := Install(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !result.KeptExisting || result.DesktopFile != path {
		t.Fatalf("result = %+v, want the user's entry kept", result)
	}
	if got, _ := os.ReadFile(path); string(got) != own {
		t.Fatalf("user's entry was modified:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(apps, "whatthedock.desktop")); err == nil {
		t.Fatal("a second entry was created next to the user's")
	}
	if len(result.Icons) != 2 {
		t.Fatalf("icons written = %v, want both (the user's entry points at them)", result.Icons)
	}
}

func TestEntryQuotesTheExecPath(t *testing.T) {
	cases := map[string]string{
		"/home/me/my apps/whatthedock": `Exec="/home/me/my apps/whatthedock"`,
		`/tmp/a"b`:                     `Exec="/tmp/a\\"b"`,
		"/tmp/$x":                      `Exec="/tmp/\\$x"`,
		"/tmp/100%":                    `Exec="/tmp/100%%"`,
		`/tmp/a\b`:                     `Exec="/tmp/a\\\\b"`,
	}
	for exe, want := range cases {
		if got := Entry(exe); !strings.Contains(got, want+"\n") {
			t.Errorf("Entry(%q) = %q, want a line %q", exe, got, want)
		}
	}
}

func TestInstallRejectsARelativeExecutable(t *testing.T) {
	opts := testOptions(t)
	opts.Exe = "whatthedock"
	if _, err := Install(opts); err == nil {
		t.Fatal("a relative Exec path would break once launched from elsewhere")
	}
}
