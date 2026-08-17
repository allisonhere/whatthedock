package ui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/whatthedock/internal/ai"
	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/domain"
)

// aiAnalyzeTimeout bounds a single "analyze with AI" request — generous
// enough for a slower model/provider, short enough that a hung request
// doesn't leave the busy state stuck indefinitely. Covers both the log
// fetch and the AI call, sequentially, inside the one command.
const aiAnalyzeTimeout = 30 * time.Second

// aiLogTailLines/aiLogLineMaxRunes bound how much log content rides along
// in the prompt — enough recent lines to catch the error around a crash or
// restart without letting one huge line (or a very chatty container) blow
// up the request.
const (
	aiLogTailLines    = 40
	aiLogLineMaxRunes = 300
)

// aiAnalysisDoneMsg carries an AI analysis result back into Update. id is
// the problem row it was requested for — compared against
// Model.aiAnalysisFor before being applied, so a response for a row the
// user has since moved away from (having pressed "a" again on a different
// row, which advances aiAnalysisFor) is silently discarded rather than
// overwriting a newer, still-in-flight request's eventual result.
type aiAnalysisDoneMsg struct {
	id     domain.ResourceID
	result string
	err    error
}

// startAIAnalysis dispatches an "analyze with AI" request for whichever
// problem row is currently selected — a no-op if the Problems list is
// empty, matching currentProblem's own fallback shape.
func (m Model) startAIAnalysis() (tea.Model, tea.Cmd) {
	current := m.currentProblem(m.snapshotProblems())
	if current == nil {
		return m, nil
	}
	m.aiAnalyzing = true
	m.aiAnalysisErr = nil
	m.aiAnalysis = ""
	m.aiAnalysisFor = current.id
	return m, m.aiAnalyzeCmd(*current)
}

// aiAnalyzeCmd resolves the configured AI provider and, if one is actually
// usable, fetches the container's recent logs and calls the provider with
// a prompt built from row's data plus that log tail. An unconfigured
// provider (no API key/base URL anywhere) never makes a network call —
// aiConfig's error becomes the result immediately, same shape as a real
// provider failure so the render path doesn't need to special-case it.
func (m Model) aiAnalyzeCmd(row problemRow) tea.Cmd {
	id := row.id
	cfg, err := m.aiConfig()
	if err != nil {
		return func() tea.Msg { return aiAnalysisDoneMsg{id: id, err: err} }
	}
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), aiAnalyzeTimeout)
		defer cancel()
		logLines := fetchRecentLogLines(ctx, provider, id, aiLogTailLines)
		prompt := aiAnalysisPrompt(row, logLines)
		text, err := ai.Analyze(ctx, cfg, prompt)
		return aiAnalysisDoneMsg{id: id, result: text, err: err}
	}
}

// fetchRecentLogLines best-effort tails a container's most recent log
// lines for grounding the AI prompt — logs are usually the actual evidence
// for what went wrong, not just the state whatthedock already tracks. A
// failure here (container already removed, log driver doesn't support
// reading, etc.) degrades to analyzing without logs rather than failing
// the whole request, matching the graceful-degradation used elsewhere in
// this codebase (e.g. the app log's own best-effort file write) — the
// rule-based insight and container state alone are still useful context.
func fetchRecentLogLines(ctx context.Context, provider app.Provider, id domain.ResourceID, maxLines int) []string {
	stream, err := provider.Logs(ctx, id, app.LogOptions{Tail: strconv.Itoa(maxLines), Follow: false})
	if err != nil {
		return nil
	}
	defer stream.Close()
	scanner := bufio.NewScanner(stream)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	lines := make([]string, 0, maxLines)
	for scanner.Scan() && len(lines) < maxLines {
		lines = append(lines, truncateRunes(cleanDockerLogLine(scanner.Text()), aiLogLineMaxRunes))
	}
	return lines
}

// truncateRunes shortens s to at most n runes, appending an ellipsis when
// it actually had to cut anything — same "signal that truncation happened"
// principle as wrapInsightText, just without that function's word-boundary
// wrapping (a single overlong log line, not prose, doesn't need it).
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// aiConfig builds an ai.Config from Settings, resolving the API key by
// checking that provider's own standard env var first — ANTHROPIC_API_KEY/
// OPENAI_API_KEY/GEMINI_API_KEY — before falling back to the stored
// Settings value, so exporting one of these already works with no Settings
// change at all, and storing a key in Settings is opt-in rather than the
// only path. Returns an error (never a zero Config) when nothing usable is
// configured, so the caller never has to guess whether Analyze would
// actually work.
func (m Model) aiConfig() (ai.Config, error) {
	provider := m.settings.AIProvider
	apiKey := m.settings.AIAPIKey
	if envVar := aiEnvVarFor(provider); envVar != "" {
		if key := strings.TrimSpace(os.Getenv(envVar)); key != "" {
			apiKey = key
		}
	}
	cfg := ai.Config{
		Provider: ai.Provider(provider.String()),
		Model:    m.settings.AIModel,
		BaseURL:  m.settings.AIBaseURL,
		APIKey:   apiKey,
	}
	if provider == aiProviderCustom {
		if strings.TrimSpace(cfg.BaseURL) == "" {
			return ai.Config{}, errors.New("no AI provider configured — set a base URL for the custom provider in Settings")
		}
		return cfg, nil
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return ai.Config{}, fmt.Errorf("no AI provider configured — set an API key in Settings or export %s", aiEnvVarFor(provider))
	}
	return cfg, nil
}

// aiEnvVarFor is the standard API key env var aiConfig checks before a
// stored Settings value — empty for ProviderCustom, which has no such
// standard (self-hosted OpenAI-compatible servers vary, and plenty don't
// require a key at all; see internal/ai's requireKey=false for custom).
func aiEnvVarFor(p aiProvider) string {
	switch p {
	case aiProviderOpenAI:
		return "OPENAI_API_KEY"
	case aiProviderGemini:
		return "GEMINI_API_KEY"
	case aiProviderAnthropic:
		return "ANTHROPIC_API_KEY"
	default:
		return ""
	}
}

// aiAnalysisPrompt builds the prompt sent to Analyze from row's own data,
// problemInsight's rule-based read on it, and a recent log tail (see
// fetchRecentLogLines) — grounding the AI in what whatthedock already
// deduced plus the actual evidence for what went wrong, instead of asking
// it to guess from container state alone. Kept here (not internal/ai)
// since prompt phrasing is a UI/product concern; internal/ai stays a dumb
// HTTP client with no opinion on wording.
func aiAnalysisPrompt(row problemRow, logLines []string) string {
	ctr := row.container
	var b strings.Builder
	b.WriteString("You are helping diagnose a Docker container problem inside a terminal UI called whatthedock. ")
	b.WriteString("Answer in 3-6 concise, specific, actionable sentences — plain prose, no markdown headers or bullet lists.\n\n")
	fmt.Fprintf(&b, "Container: %s\n", row.name)
	if ctr.Image != "" {
		fmt.Fprintf(&b, "Image: %s\n", ctr.Image)
	}
	fmt.Fprintf(&b, "State: %s\n", ctr.State)
	if strings.TrimSpace(ctr.Status) != "" {
		fmt.Fprintf(&b, "Status: %s\n", ctr.Status)
	}
	fmt.Fprintf(&b, "Health: %s\n", ctr.Health)
	fmt.Fprintf(&b, "Restart count: %d\n", ctr.RestartCount)
	if ctr.RestartPolicy != "" {
		fmt.Fprintf(&b, "Restart policy: %s\n", ctr.RestartPolicy)
	}
	fmt.Fprintf(&b, "Detected problem: %s\n", row.detail)
	fmt.Fprintf(&b, "\nWhatTheDock's own rule-based read on this:\n%s\n", problemInsight(row))
	if len(logLines) > 0 {
		fmt.Fprintf(&b, "\nMost recent log lines (oldest first, up to %d):\n", aiLogTailLines)
		for _, line := range logLines {
			b.WriteString(line)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("\n(No recent log lines were available.)\n")
	}
	b.WriteString("\nGiven this — especially the logs, if present — what's the most likely root cause and what should the operator check or do next?")
	return b.String()
}
