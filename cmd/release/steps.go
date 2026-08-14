package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// releasePlanSteps returns the checklist shown on screenConfirm/screenRunning
// — must list the same steps, in the same order, that runReleaseCmd actually
// executes.
func releasePlanSteps(version string) []stepStatus {
	labels := []string{
		"Build " + releaseBinaryName(version),
		"Tag " + version,
		"Push tag to origin",
		"Publish GitHub release",
	}
	steps := make([]stepStatus, len(labels))
	for i, l := range labels {
		steps[i] = stepStatus{label: l}
	}
	return steps
}

func releaseBinaryName(version string) string {
	return fmt.Sprintf("whatthedock-%s-%s-%s", version, runtime.GOOS, runtime.GOARCH)
}

// runReleaseCmd runs every release step in order on its own goroutine (this
// closure already runs off the Bubble Tea event loop's goroutine, the same
// as every other long-running tea.Cmd in this codebase), sending a
// stepEvent before and after each step into events. It stops at the first
// error rather than continuing partway through a broken release.
func runReleaseCmd(version string, dryRun bool, events chan stepEvent) tea.Cmd {
	return func() tea.Msg {
		defer close(events)
		binName := releaseBinaryName(version)
		binPath := "dist/" + binName

		run := func(index int, label string, fn func() (string, error)) bool {
			events <- stepEvent{index: index, label: label, starting: true}
			out, err := fn()
			events <- stepEvent{index: index, label: label, output: out, err: err}
			return err == nil
		}

		ok := run(0, "Build "+binName, func() (string, error) {
			return buildBinary(version, binPath)
		})
		if !ok {
			events <- stepEvent{done: true}
			return nil
		}

		ok = run(1, "Tag "+version, func() (string, error) {
			if dryRun {
				return "(dry run) git tag " + version, nil
			}
			return runCmd("git", "tag", version)
		})
		if !ok {
			events <- stepEvent{done: true}
			return nil
		}

		ok = run(2, "Push tag to origin", func() (string, error) {
			if dryRun {
				return "(dry run) git push origin " + version, nil
			}
			return runCmd("git", "push", "origin", version)
		})
		if !ok {
			events <- stepEvent{done: true}
			return nil
		}

		run(3, "Publish GitHub release", func() (string, error) {
			if dryRun {
				return "(dry run) gh release create " + version + " " + binPath + " --title " + version + " --generate-notes", nil
			}
			return runCmd("gh", "release", "create", version, binPath, "--title", version, "--generate-notes")
		})

		events <- stepEvent{done: true}
		return nil
	}
}

func buildBinary(version, outPath string) (string, error) {
	if err := os.MkdirAll("dist", 0o755); err != nil {
		return "", err
	}
	commit, _ := runCmd("git", "rev-parse", "--short", "HEAD")
	commit = strings.TrimSpace(commit)
	date := time.Now().UTC().Format("2006-01-02")
	ldflags := fmt.Sprintf("-X main.version=%s -X main.commit=%s -X main.date=%s", version, commit, date)
	return runCmd("go", "build", "-buildvcs=false", "-ldflags", ldflags, "-o", outPath, "./cmd/whatthedock")
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// currentBranch, isDirty, and latestTag gather repo state shown on
// screenVersion — run synchronously before the Bubble Tea program starts
// since they're fast, local, read-only git calls.
func currentBranch() string {
	out, err := runCmd("git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "?"
	}
	return out
}

func isDirty() bool {
	out, err := runCmd("git", "status", "--porcelain")
	return err == nil && out != ""
}

func latestTag() string {
	out, err := runCmd("git", "tag", "--list", "v*")
	if err != nil || out == "" {
		return ""
	}
	tags := strings.Split(out, "\n")
	sort.Slice(tags, func(i, j int) bool { return compareVersions(tags[i], tags[j]) > 0 })
	return tags[0]
}

// commitLog returns the last n "<short-sha> <subject>" lines, most recent
// first — shown on screenVersion so there's real repo content to look at
// the moment the tool opens, not just a bare version prompt.
func commitLog(n int) []string {
	out, err := runCmd("git", "log", fmt.Sprintf("-%d", n), "--pretty=format:%h %s")
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// commitsSince returns commitLog-style lines for every commit after ref
// (typically the latest tag) up to HEAD — what would actually go into this
// release. Falls back to commitLog if ref is empty (no prior tag yet).
func commitsSince(ref string, max int) []string {
	if ref == "" {
		return commitLog(max)
	}
	out, err := runCmd("git", "log", ref+"..HEAD", "--pretty=format:%h %s")
	if err != nil || out == "" {
		return nil
	}
	lines := strings.Split(out, "\n")
	if len(lines) > max {
		lines = lines[:max]
	}
	return lines
}

// diffStatSince returns the summary line of `git diff --stat` between ref
// and HEAD (e.g. "14 files changed, 823 insertions(+), 41 deletions(-)"),
// empty if ref is empty or there's no diff.
func diffStatSince(ref string) string {
	if ref == "" {
		return ""
	}
	out, err := runCmd("git", "diff", "--shortstat", ref+"..HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// suggestNextVersion bumps the patch component of latest ("v1.2.3" ->
// "v1.2.4"). Falls back to "v0.1.0" for anything that isn't a plain
// vMAJOR.MINOR.PATCH tag rather than guessing at a more elaborate scheme.
func suggestNextVersion(latest string) string {
	major, minor, patch, ok := parseVersion(latest)
	if !ok {
		return "v0.1.0"
	}
	return fmt.Sprintf("v%d.%d.%d", major, minor, patch+1)
}

func parseVersion(v string) (major, minor, patch int, ok bool) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, 0, 0, false
		}
		nums[i] = n
	}
	return nums[0], nums[1], nums[2], true
}

func compareVersions(a, b string) int {
	aMaj, aMin, aPatch, aOK := parseVersion(a)
	bMaj, bMin, bPatch, bOK := parseVersion(b)
	if !aOK || !bOK {
		return strings.Compare(a, b)
	}
	for _, pair := range [][2]int{{aMaj, bMaj}, {aMin, bMin}, {aPatch, bPatch}} {
		if pair[0] != pair[1] {
			return pair[0] - pair[1]
		}
	}
	return 0
}
