// Command release is a small TUI for publishing a WhatTheDock GitHub
// release: builds the version-stamped binary the same way the README's
// manual build instructions do, signs it, tags the commit, pushes the
// tag, and publishes a GitHub release with the binary and its detached
// signature attached via gh.
//
// Run it from the repository root:
//
//	go run ./cmd/release
//	go run ./cmd/release -dry-run          # walk through every step without tagging, pushing, or publishing
//	go run ./cmd/release -genkey           # one-time: generate the release signing keypair
//	go run ./cmd/release -auto             # no TUI: auto-versioned, unattended (see runAuto) — also ./release.sh
//	go run ./cmd/release -auto -version=vX.Y.Z  # -auto with an explicit version instead of the auto-suggested one
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	dryRun := false
	auto := false
	versionOverride := ""
	for _, arg := range os.Args[1:] {
		switch {
		case arg == "-dry-run" || arg == "--dry-run":
			dryRun = true
		case arg == "-auto" || arg == "--auto":
			auto = true
		case arg == "-genkey" || arg == "--genkey":
			if err := genKey(os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "release:", err)
				os.Exit(1)
			}
			return
		case strings.HasPrefix(arg, "-version=") || strings.HasPrefix(arg, "--version="):
			versionOverride = arg[strings.Index(arg, "=")+1:]
		}
	}

	if err := preflight(); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}

	if auto {
		version := versionOverride
		if version == "" {
			version = suggestNextVersion(latestTag())
		}
		if err := runAuto(version, dryRun); err != nil {
			fmt.Fprintln(os.Stderr, "release:", err)
			os.Exit(1)
		}
		return
	}

	branch := currentBranch()
	dirty := isDirty()
	latest := latestTag()
	suggested := suggestNextVersion(latest)
	if versionOverride != "" {
		suggested = versionOverride
	}
	commits := commitsSince(latest, 12)
	diffStat := diffStatSince(latest)

	m := initialModel(branch, dirty, latest, suggested, commits, diffStat, dryRun)
	program := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

// runAuto is -auto's non-interactive path: no TUI, no confirmation
// prompt, auto-suggested (or explicitly passed) version, reusing
// runReleaseCmd unchanged — same steps, same order, same signing
// requirement as the interactive path, just driven and printed plainly
// instead of through the Bubble Tea model. Refuses a dirty working tree:
// the interactive path shows that on screenVersion and lets a human
// decide whether to proceed anyway; nothing here is asking that question,
// so it hard-fails instead of silently releasing from uncommitted state.
func runAuto(version string, dryRun bool) error {
	if isDirty() {
		return fmt.Errorf("working tree is dirty — commit or stash before an unattended release (see git status)")
	}
	fmt.Println("whatthedock release", version)
	if dryRun {
		fmt.Println("(dry run — nothing will be tagged, pushed, or published)")
	}
	events := make(chan stepEvent, 32)
	cmd := runReleaseCmd(version, dryRun, events)
	go cmd()

	failed := false
	for ev := range events {
		if ev.done {
			break
		}
		if ev.starting {
			fmt.Println("→", ev.label)
			continue
		}
		if ev.err != nil {
			failed = true
			fmt.Println("✗", ev.label+":", ev.err.Error())
		} else {
			fmt.Println("✓", ev.label)
		}
		if ev.output != "" {
			for _, line := range strings.Split(ev.output, "\n") {
				fmt.Println("   ", line)
			}
		}
	}
	if failed {
		return fmt.Errorf("release failed")
	}
	fmt.Println("done.")
	return nil
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
