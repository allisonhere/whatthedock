package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allisonhere/whatthedock/internal/config"
)

func TestWriteComposeTempKeepsRestrictiveMode(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(base, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	temp := base + ".tmp"
	// A stale temp file with a wider mode must not leak that mode through.
	if err := os.WriteFile(temp, []byte("stale"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := writeComposeTemp(temp, []byte("new\n"), base); err != nil {
		t.Fatal(err)
	}
	if err := promoteComposeTemp(temp, base); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode after rewrite = %o, want 600", got)
	}
}

func TestWriteComposeTempNewFileDefaultsTo0644(t *testing.T) {
	dir := t.TempDir()
	temp := filepath.Join(dir, "new.yaml.tmp")
	if err := writeComposeTemp(temp, []byte("x"), filepath.Join(dir, "missing.yaml")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(temp)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got&0o600 != 0o600 || got&0o022 != 0 {
		t.Fatalf("mode = %o, want a normal umask-limited 0644 default", got)
	}
}

func TestPromoteComposeTempWritesThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.yaml")
	link := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(real, []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	temp := link + ".tmp"
	if err := writeComposeTemp(temp, []byte("new\n"), link); err != nil {
		t.Fatal(err)
	}
	if err := promoteComposeTemp(temp, link); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("compose.yaml is no longer a symlink (info=%v err=%v)", info, err)
	}
	got, err := os.ReadFile(real)
	if err != nil || string(got) != "new\n" {
		t.Fatalf("symlink target = %q, %v; want updated content", got, err)
	}
}

func TestRemoteWriteComposeTempSeedsModeFromReference(t *testing.T) {
	var gotScript, gotStdin string
	orig := sshRun
	sshRun = func(_ context.Context, _ config.System, script, stdin string) ([]byte, error) {
		gotScript, gotStdin = script, stdin
		return nil, nil
	}
	t.Cleanup(func() { sshRun = orig })

	if err := remoteWriteComposeTemp(context.Background(), config.System{}, "/srv/a b/compose.yaml.tmp", "body", "/srv/a b/compose.yaml"); err != nil {
		t.Fatal(err)
	}
	if gotStdin != "body" {
		t.Fatalf("stdin = %q, want the file content", gotStdin)
	}
	for _, want := range []string{"cp -p", "'/srv/a b/compose.yaml'", "cat > '/srv/a b/compose.yaml.tmp'"} {
		if !strings.Contains(gotScript, want) {
			t.Fatalf("script %q missing %q", gotScript, want)
		}
	}
	if strings.Index(gotScript, "cp -p") > strings.Index(gotScript, "cat >") {
		t.Fatalf("script %q must copy mode before writing content", gotScript)
	}
}
