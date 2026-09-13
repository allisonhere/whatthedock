package ui

import (
	"fmt"
	"path"
	"strings"

	"github.com/allisonhere/whatthedock/internal/config"
)

// diffLines computes an LCS-based, line-oriented diff of before→after. Each
// returned line is prefixed with "  " (unchanged), "- " (removed), or "+ "
// (added). It's deliberately a small self-contained implementation rather
// than a dependency: it exists to show a human what an apply will change on
// the confirm screen, not to emit a machine-applicable patch.
func diffLines(before, after []string) []string {
	n, m := len(before), len(after)
	// lcs[i][j] is the LCS length of before[i:] and after[j:]; filling it
	// backwards lets the forward walk below emit changes in file order.
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case before[i] == after[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	out := make([]string, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case before[i] == after[j]:
			out = append(out, "  "+before[i])
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, "- "+before[i])
			i++
		default:
			out = append(out, "+ "+after[j])
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, "- "+before[i])
	}
	for ; j < m; j++ {
		out = append(out, "+ "+after[j])
	}
	return out
}

// diffFileLines splits content into diffable lines. An empty document yields
// no lines at all (rather than one empty line), so a brand-new file shows
// purely additions.
func diffFileLines(content string) []string {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(content, "\n"), "\n")
}

// composeChangePreview describes what applying d would write: the target file
// and the before/after text, derived entirely from data already on the draft
// — no filesystem or SSH I/O — so the confirm screen can render it instantly.
// ok is false when there's nothing meaningful to diff (standalone drafts). It
// mirrors the real apply paths: a service already in the base file is
// field-merged into it, an existing generated override is rewritten, and a
// brand-new service gets a fresh override (or base file, when adopting).
func (d createDraft) composeChangePreview(system config.System) (target, before, after string, ok bool) {
	if d.Mode != createModeCompose {
		return "", "", "", false
	}
	spec, err := d.ComposeSpec(system)
	if err != nil {
		return "", "", "", false
	}
	if d.IsStack() {
		return spec.BaseFile, d.OverrideRaw, spec.Content, true
	}
	switch {
	case d.BaseFileMissing:
		return spec.BaseFile, "", spec.Content, true
	case d.OverrideRawBase && d.OverrideRawSet:
		// Service lives in the base file: apply merges the draft's fields
		// into its existing block (unless nothing was edited, in which case
		// the file is written back unchanged).
		if spec.FullBase {
			return spec.BaseFile, d.OverrideRaw, spec.Content, true
		}
		fields, fieldsOK := composeServiceFieldsFromContent(spec.Content, spec.Service)
		if !fieldsOK {
			return spec.BaseFile, d.OverrideRaw, spec.Content, true
		}
		merged, mergeErr := mergeComposeServiceFields([]byte(d.OverrideRaw), spec.Service, fields, spec.ChangedFields)
		if mergeErr != nil {
			return spec.BaseFile, d.OverrideRaw, spec.Content, true
		}
		return spec.BaseFile, d.OverrideRaw, string(merged), true
	case d.OverrideRawSet:
		return spec.OverrideFile, d.OverrideRaw, spec.Content, true
	default:
		return spec.OverrideFile, "", spec.Content, true
	}
}

// prepareComposeConfirmDiff caches the confirm screen's before→after diff on
// the draft when the prompt opens, so View doesn't recompute an O(n·m) diff
// on every render tick. It also records the target path so the confirm prompt
// can name the file actually being written (base file for a merge, override
// for a brand-new service).
func (m *Model) prepareComposeConfirmDiff() {
	m.createDraft.confirmDiffPath = ""
	m.createDraft.confirmDiffLabel = ""
	m.createDraft.confirmDiffLines = nil
	target, before, after, ok := m.createDraft.composeChangePreview(m.activeSystemConfig())
	if !ok {
		return
	}
	m.createDraft.confirmDiffPath = target
	m.createDraft.confirmDiffLabel = path.Base(target)
	m.createDraft.confirmDiffLines = diffLines(diffFileLines(before), diffFileLines(after))
}

// renderConfirmDiff formats a cached diff for the confirm screen: a header
// naming the target file, a one-line summary, then the changed lines
// themselves (unchanged context is omitted so a large file can't push the
// actual change out of view), capped with an explicit note when there are
// more changes than fit.
func renderConfirmDiff(label string, lines []string, maxLines int) string {
	var added, removed int
	changed := make([]string, 0, len(lines))
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+ "):
			added++
			changed = append(changed, line)
		case strings.HasPrefix(line, "- "):
			removed++
			changed = append(changed, line)
		}
	}

	out := []string{"Changes to " + label}
	if added == 0 && removed == 0 {
		out = append(out, "  (no changes — this will re-run compose up only)")
		return strings.Join(out, "\n")
	}
	out = append(out, fmt.Sprintf("  %d added, %d removed", added, removed))

	body := changed
	if maxLines > 2 && len(body) > maxLines-2 {
		hidden := len(body) - (maxLines - 2)
		body = append(body[:maxLines-2], fmt.Sprintf("  … %d more changed lines — esc back to review", hidden))
	}
	return strings.Join(append(out, body...), "\n")
}
