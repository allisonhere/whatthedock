// Command release is a small TUI for publishing a WhatTheDock GitHub
// release: builds the version-stamped binary the same way the README's
// manual build instructions do, tags the commit, pushes the tag, and
// publishes a GitHub release with the binary attached via gh.
//
// Run it from the repository root:
//
//	go run ./cmd/release
//	go run ./cmd/release -dry-run   # walk through every step without tagging, pushing, or publishing
package main

import (
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	dryRun := false
	for _, arg := range os.Args[1:] {
		if arg == "-dry-run" || arg == "--dry-run" {
			dryRun = true
		}
	}

	if err := preflight(); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}

	branch := currentBranch()
	dirty := isDirty()
	latest := latestTag()
	suggested := suggestNextVersion(latest)
	commits := commitsSince(latest, 12)
	diffStat := diffStatSince(latest)

	m := initialModel(branch, dirty, latest, suggested, commits, diffStat, dryRun)
	program := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

// preflight checks the two external tools every step depends on exist, and
// that this is actually being run from a git checkout — cheap checks that
// turn "step 3 of 4 fails with a confusing exec error" into a clear message
// before the TUI even opens.
func preflight() error {
	for _, tool := range []string{"git", "gh", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s not found on PATH (required)", tool)
		}
	}
	if _, err := os.Stat(".git"); err != nil {
		return fmt.Errorf("run this from the repository root (no .git directory here)")
	}
	if _, err := os.Stat("cmd/whatthedock"); err != nil {
		return fmt.Errorf("run this from the whatthedock repository root (no cmd/whatthedock here)")
	}
	return nil
}
