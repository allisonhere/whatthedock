package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/systems"
)

// composeBackupGlob is the sibling glob every pre-apply snapshot matches.
// Kept in one place so the writer (create.go) and the reader here agree.
func composeBackupGlob(base string) string {
	return base + ".whatthedock-*.bak"
}

// newestComposeBackup returns the most recent path from a list of backup
// paths (zero-padded timestamps sort chronologically), or "" when empty.
func newestComposeBackup(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	sort.Strings(paths)
	return paths[len(paths)-1]
}

// composeRestoreDoneMsg carries the outcome of a restore back into Update;
// base/backup are set on success so the status line can name both.
type composeRestoreDoneMsg struct {
	base   string
	backup string
	err    error
}

// restoreComposeBackupCmd restores base to its most recent WhatTheDock
// pre-apply backup. It snapshots the current contents first (so the restore
// itself is reversible) and only writes the file — the caller still has to
// bring the service up to match it.
func restoreComposeBackupCmd(system config.System, base string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if strings.TrimSpace(base) == "" {
			return composeRestoreDoneMsg{err: errors.New("no compose file to restore")}
		}
		if system.Kind == "ssh" {
			backup, err := restoreComposeBackupRemote(ctx, system, base)
			return composeRestoreDoneMsg{base: base, backup: backup, err: err}
		}
		backup, err := restoreComposeBackupLocal(base)
		return composeRestoreDoneMsg{base: base, backup: backup, err: err}
	}
}

func restoreComposeBackupLocal(base string) (string, error) {
	matches, err := filepath.Glob(composeBackupGlob(base))
	if err != nil {
		return "", err
	}
	backup := newestComposeBackup(matches)
	if backup == "" {
		return "", fmt.Errorf("no WhatTheDock backup found for %s", base)
	}
	content, err := os.ReadFile(backup)
	if err != nil {
		return "", err
	}
	// Snapshot the current (presumably broken) file so this restore is
	// itself reversible.
	if err := backupComposeBase(base); err != nil {
		return "", err
	}
	temp := base + ".tmp"
	if err := os.WriteFile(temp, content, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(temp, base); err != nil {
		_ = os.Remove(temp)
		return "", err
	}
	return backup, nil
}

func restoreComposeBackupRemote(ctx context.Context, system config.System, base string) (string, error) {
	out, err := sshRun(ctx, system, "ls -1 "+systems.ShellQuote(base)+".whatthedock-*.bak", "")
	var matches []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			matches = append(matches, line)
		}
	}
	backup := newestComposeBackup(matches)
	if err != nil || backup == "" {
		return "", fmt.Errorf("no WhatTheDock backup found for %s", base)
	}
	content, err := sshRun(ctx, system, "cat "+systems.ShellQuote(backup), "")
	if err != nil {
		return "", err
	}
	if err := backupComposeBaseRemote(ctx, system, base); err != nil {
		return "", err
	}
	temp := base + ".tmp"
	if _, err := sshRun(ctx, system, "cat > "+systems.ShellQuote(temp), string(content)); err != nil {
		return "", err
	}
	if _, err := sshRun(ctx, system, "mv "+systems.ShellQuote(temp)+" "+systems.ShellQuote(base), ""); err != nil {
		_, _ = sshRun(ctx, system, "rm -f "+systems.ShellQuote(temp), "")
		return "", err
	}
	return backup, nil
}

// openRestoreComposeConfirm opens the confirm for the selected Compose
// service's base file. It resolves the target synchronously (just the path —
// whether a backup exists is decided when the command runs, since that may be
// a remote round trip).
func (m Model) openRestoreComposeConfirm() (tea.Model, tea.Cmd) {
	selected := m.selectedContainer()
	if selected == nil {
		m.status, m.statusErr = "no container selected", true
		return m, nil
	}
	files := splitComposeConfigFiles(selected.Compose.ConfigFiles)
	if len(files) == 0 {
		m.status, m.statusErr = "selected container is not a Compose service with a config file", true
		return m, nil
	}
	m.restoreComposeBase = files[0]
	m.overlay = overlayRestoreComposeConfirm
	return m, nil
}

// handleRestoreComposeConfirmKey gates the restore the same way Delete does:
// esc/n/q cancels, y restores.
func (m Model) handleRestoreComposeConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "n", "q":
		m.overlay = overlayNone
		m.restoreComposeBase = ""
	case "y":
		base := m.restoreComposeBase
		system := m.activeSystemConfig()
		m.overlay = overlayNone
		m.restoreComposeBase = ""
		m.busy = true
		m.status, m.statusErr = "restoring "+filepath.Base(base)+"…", false
		return m, restoreComposeBackupCmd(system, base)
	}
	return m, nil
}
