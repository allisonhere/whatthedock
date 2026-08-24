package ui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/tideui"
	"github.com/allisonhere/whatthedock/internal/ai"
	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/domain"
)

func TestAIConfigMissingProviderReturnsError(t *testing.T) {
	model := testModel()
	model.settings.AIProvider = aiProviderAnthropic
	model.settings.AIAPIKey = ""

	_, err := model.aiConfig()
	if err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("aiConfig() error = %v, want it to name the env var to export", err)
	}
}

func TestAIConfigUsesStoredKeyWhenNoEnvVarSet(t *testing.T) {
	model := testModel()
	model.settings.AIProvider = aiProviderOpenAI
	model.settings.AIAPIKey = "sk-stored"

	cfg, err := model.aiConfig()
	if err != nil {
		t.Fatalf("aiConfig() error = %v", err)
	}
	if cfg.APIKey != "sk-stored" || cfg.Provider != ai.ProviderOpenAI {
		t.Fatalf("cfg = %#v, want APIKey=sk-stored Provider=openai", cfg)
	}
}

// TestAIConfigPrefersEnvVarOverStoredKey is the regression test for the
// env-var-first precedence this session's plan called for: exporting the
// provider's own standard env var must win over whatever's stored in
// Settings, not the other way around.
func TestAIConfigPrefersEnvVarOverStoredKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-from-env")
	model := testModel()
	model.settings.AIProvider = aiProviderAnthropic
	model.settings.AIAPIKey = "sk-from-settings"

	cfg, err := model.aiConfig()
	if err != nil {
		t.Fatalf("aiConfig() error = %v", err)
	}
	if cfg.APIKey != "sk-from-env" {
		t.Fatalf("APIKey = %q, want the env var to take precedence over the stored key", cfg.APIKey)
	}
}

func TestAIConfigCustomRequiresBaseURLNotAPIKey(t *testing.T) {
	model := testModel()
	model.settings.AIProvider = aiProviderCustom
	model.settings.AIBaseURL = ""

	if _, err := model.aiConfig(); err == nil || !strings.Contains(err.Error(), "base URL") {
		t.Fatalf("aiConfig() error = %v, want a clear no-base-URL error for an empty custom config", err)
	}

	model.settings.AIBaseURL = "http://localhost:11434/v1"
	cfg, err := model.aiConfig()
	if err != nil {
		t.Fatalf("aiConfig() error = %v, want no error once a base URL is set (custom doesn't require a key)", err)
	}
	if cfg.Provider != ai.ProviderCustom || cfg.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("cfg = %#v, want Provider=custom BaseURL set", cfg)
	}
}

func TestAIEnvVarForEachProvider(t *testing.T) {
	cases := map[aiProvider]string{
		aiProviderAnthropic: "ANTHROPIC_API_KEY",
		aiProviderOpenAI:    "OPENAI_API_KEY",
		aiProviderGemini:    "GEMINI_API_KEY",
		aiProviderCustom:    "",
	}
	for provider, want := range cases {
		if got := aiEnvVarFor(provider); got != want {
			t.Fatalf("aiEnvVarFor(%v) = %q, want %q", provider, got, want)
		}
	}
}

func TestAIAnalysisPromptIncludesContainerDataAndInsight(t *testing.T) {
	row := problemRow{
		id:     domain.ResourceID{Host: "local", ID: "1"},
		name:   "media-postgres-1",
		detail: "unhealthy",
		container: domain.Container{
			Name: "media-postgres-1", Image: "postgres:15", State: domain.StateRunning,
			Health: domain.HealthUnhealthy, RestartCount: 3, RestartPolicy: "unless-stopped",
		},
	}
	prompt := aiAnalysisPrompt(row, nil)
	for _, want := range []string{"media-postgres-1", "postgres:15", "unhealthy", "3", "unless-stopped", problemInsight(row)} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if !strings.Contains(prompt, "No recent log lines were available") {
		t.Fatalf("prompt with nil logLines missing the no-logs note:\n%s", prompt)
	}
}

// TestAIAnalysisPromptIncludesLogLines is the regression test for the
// feature reported live: the AI needs actual log content to diagnose a
// problem, not just container state — logLines must appear verbatim in
// the prompt when present.
func TestAIAnalysisPromptIncludesLogLines(t *testing.T) {
	row := problemRow{name: "media-postgres-1", container: domain.Container{Health: domain.HealthUnhealthy}}
	logs := []string{"2026-08-17T12:00:00Z FATAL: could not connect to database", "2026-08-17T12:00:01Z retrying in 5s"}

	prompt := aiAnalysisPrompt(row, logs)
	for _, want := range logs {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing log line %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "No recent log lines were available") {
		t.Fatalf("prompt claims no logs were available despite logLines being non-empty:\n%s", prompt)
	}
}

func TestTruncateRunesLeavesShortStringsUnchanged(t *testing.T) {
	if got := truncateRunes("short", 10); got != "short" {
		t.Fatalf("truncateRunes() = %q, want unchanged", got)
	}
}

func TestTruncateRunesAddsEllipsisWhenCut(t *testing.T) {
	got := truncateRunes("this is a long log line", 10)
	if len([]rune(got)) != 11 { // 10 runes + the ellipsis rune
		t.Fatalf("truncateRunes() = %q (len %d), want 10 runes + ellipsis", got, len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncateRunes() = %q, want it to end with an ellipsis", got)
	}
}

// TestFetchRecentLogLinesReturnsNilOnError guards the graceful-degradation
// path: a container whose logs can't be fetched (removed, unsupported log
// driver, etc.) must not fail the whole analysis — nil logs, not an error.
func TestFetchRecentLogLinesReturnsNilOnError(t *testing.T) {
	provider := &erroringLogsProvider{}
	got := fetchRecentLogLines(context.Background(), provider, domain.ResourceID{Host: "local", ID: "1"}, aiLogTailLines)
	if got != nil {
		t.Fatalf("fetchRecentLogLines() = %#v, want nil when the provider's Logs call fails", got)
	}
}

// TestFetchRecentLogLinesCleansAndCapsLines checks the multiplexed-stream
// header stripping (cleanDockerLogLine) and the maxLines cap both apply.
func TestFetchRecentLogLinesCleansAndCapsLines(t *testing.T) {
	raw := ""
	for i := 0; i < 5; i++ {
		raw += "\x01\x00\x00\x00\x00\x00\x00\x08line " + strconv.Itoa(i) + "\n"
	}
	provider := &fakeLogsProvider{content: raw}
	got := fetchRecentLogLines(context.Background(), provider, domain.ResourceID{Host: "local", ID: "1"}, 3)
	if len(got) != 3 {
		t.Fatalf("fetchRecentLogLines() returned %d lines, want capped to 3: %#v", len(got), got)
	}
	if got[0] != "line 0" {
		t.Fatalf("got[0] = %q, want the docker stream header stripped -> \"line 0\"", got[0])
	}
}

type erroringLogsProvider struct{ noopProvider }

func (p *erroringLogsProvider) Logs(context.Context, domain.ResourceID, app.LogOptions) (io.ReadCloser, error) {
	return nil, errors.New("container not found")
}

type fakeLogsProvider struct {
	noopProvider
	content string
}

func (p *fakeLogsProvider) Logs(context.Context, domain.ResourceID, app.LogOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(p.content)), nil
}

// noopProvider satisfies app.Provider with panics on every method except
// Logs, which the two fakes above override — keeps each fake's definition
// to just the one method this test actually exercises.
type noopProvider struct{}

func (noopProvider) Host() domain.Host                                 { panic("not implemented") }
func (noopProvider) Ping(context.Context) error                        { panic("not implemented") }
func (noopProvider) Snapshot(context.Context) (domain.Snapshot, error) { panic("not implemented") }
func (noopProvider) Events(context.Context) (<-chan domain.ContainerEvent, error) {
	panic("not implemented")
}
func (noopProvider) Container(context.Context, domain.ResourceID) (domain.Container, error) {
	panic("not implemented")
}
func (noopProvider) ContainerStats(context.Context, domain.ResourceID) (domain.ContainerStats, error) {
	panic("not implemented")
}
func (noopProvider) Logs(context.Context, domain.ResourceID, app.LogOptions) (io.ReadCloser, error) {
	panic("not implemented")
}
func (noopProvider) CreateContainer(context.Context, app.ContainerCreateSpec) (domain.ResourceID, error) {
	panic("not implemented")
}
func (noopProvider) StartContainer(context.Context, domain.ResourceID) error {
	panic("not implemented")
}
func (noopProvider) StopContainer(context.Context, domain.ResourceID) error { panic("not implemented") }
func (noopProvider) RestartContainer(context.Context, domain.ResourceID) error {
	panic("not implemented")
}
func (noopProvider) RemoveContainer(context.Context, domain.ResourceID, bool) error {
	panic("not implemented")
}
func (noopProvider) PullImage(context.Context, string, func(app.PullProgress)) error {
	panic("not implemented")
}
func (noopProvider) Images(context.Context) ([]domain.Image, error) { panic("not implemented") }
func (noopProvider) RemoveImage(context.Context, string) error      { panic("not implemented") }
func (noopProvider) Networks(context.Context) ([]domain.Network, error) {
	panic("not implemented")
}
func (noopProvider) RemoveNetwork(context.Context, string) error { panic("not implemented") }
func (noopProvider) Volumes(context.Context) ([]domain.Volume, error) {
	panic("not implemented")
}
func (noopProvider) RemoveVolume(context.Context, string) error { panic("not implemented") }
func (noopProvider) Close() error                               { panic("not implemented") }

// TestStartAIAnalysisNoProblemsIsNoop guards against a nil-pointer panic
// when there's nothing to analyze (currentProblem returns nil).
func TestStartAIAnalysisNoProblemsIsNoop(t *testing.T) {
	model := testModel()
	model.focus = paneActivity
	model.mode = activityProblems

	// testModel's fixture always has 2 problems (see
	// TestProblemsModeFindsUnhealthyAndStoppedContainers), so force an
	// empty snapshot to actually exercise the no-problems path.
	model.snapshot.Projects = nil
	model.snapshot.Standalone = nil

	updated, cmd := model.startAIAnalysis()
	next := updated.(Model)
	if next.aiAnalyzing {
		t.Fatal("aiAnalyzing = true with no problems to analyze, want false")
	}
	if cmd != nil {
		t.Fatal("startAIAnalysis() returned a non-nil cmd with no problems, want nil")
	}
}

func TestStartAIAnalysisSetsStateAndDispatchesCmd(t *testing.T) {
	model := testModel()
	model.focus = paneActivity
	model.mode = activityProblems
	model.problemCursor = 0
	model.settings.AIProvider = aiProviderAnthropic
	model.settings.AIAPIKey = "sk-test"

	updated, cmd := model.startAIAnalysis()
	next := updated.(Model)
	if !next.aiAnalyzing {
		t.Fatal("aiAnalyzing = false right after dispatch, want true")
	}
	if next.aiAnalysisFor.ID == "" {
		t.Fatal("aiAnalysisFor not set to the analyzed row's ID")
	}
	if cmd == nil {
		t.Fatal("startAIAnalysis() returned nil cmd, want the analyze command")
	}
}

// TestStartAIAnalysisUnconfiguredProviderStillSetsErrorImmediately checks
// pressing "a" with no provider configured doesn't hang in aiAnalyzing —
// aiConfig's error becomes the result via the same cmd/msg path a real
// provider failure would, immediately resolvable without a network call.
func TestStartAIAnalysisUnconfiguredProviderStillSetsErrorImmediately(t *testing.T) {
	model := testModel()
	model.focus = paneActivity
	model.mode = activityProblems
	model.problemCursor = 0
	model.settings.AIAPIKey = ""

	updated, cmd := model.startAIAnalysis()
	next := updated.(Model)
	if cmd == nil {
		t.Fatal("startAIAnalysis() returned nil cmd, want a cmd producing the no-provider error")
	}
	msg, ok := runCmd(t, cmd).(aiAnalysisDoneMsg)
	if !ok {
		t.Fatalf("cmd produced %#v, want aiAnalysisDoneMsg", msg)
	}
	if msg.err == nil || !strings.Contains(msg.err.Error(), "no AI provider configured") {
		t.Fatalf("msg.err = %v, want a clear no-provider-configured error", msg.err)
	}
	if msg.id != next.aiAnalysisFor {
		t.Fatalf("msg.id = %v, want it to match aiAnalysisFor = %v", msg.id, next.aiAnalysisFor)
	}
}

func TestAIAnalysisDoneMsgAppliesMatchingResult(t *testing.T) {
	model := testModel()
	id := domain.ResourceID{Host: "local", ID: "1"}
	model.aiAnalyzing = true
	model.aiAnalysisFor = id

	updated, _ := model.Update(aiAnalysisDoneMsg{id: id, result: "restart it"})
	next := updated.(Model)
	if next.aiAnalyzing {
		t.Fatal("aiAnalyzing = true after a matching done message, want false")
	}
	if next.aiAnalysis != "restart it" {
		t.Fatalf("aiAnalysis = %q, want restart it", next.aiAnalysis)
	}
}

func TestAIAnalysisDoneMsgAppliesError(t *testing.T) {
	model := testModel()
	id := domain.ResourceID{Host: "local", ID: "1"}
	model.aiAnalyzing = true
	model.aiAnalysisFor = id

	updated, _ := model.Update(aiAnalysisDoneMsg{id: id, err: errBoom})
	next := updated.(Model)
	if next.aiAnalyzing {
		t.Fatal("aiAnalyzing = true after an error done message, want false")
	}
	if next.aiAnalysisErr != errBoom {
		t.Fatalf("aiAnalysisErr = %v, want errBoom", next.aiAnalysisErr)
	}
}

// TestAIAnalysisDoneMsgIgnoresStaleResponse is the regression test for the
// plan's core safety requirement: a response for a row the cursor has since
// moved away from (a newer "a" press changed aiAnalysisFor) must never
// overwrite the newer request's eventual result.
func TestAIAnalysisDoneMsgIgnoresStaleResponse(t *testing.T) {
	model := testModel()
	oldID := domain.ResourceID{Host: "local", ID: "1"}
	newID := domain.ResourceID{Host: "local", ID: "2"}
	model.aiAnalyzing = true
	model.aiAnalysisFor = newID // a newer request is now the one in flight
	model.aiAnalysis = ""

	updated, _ := model.Update(aiAnalysisDoneMsg{id: oldID, result: "stale result"})
	next := updated.(Model)
	if next.aiAnalysis == "stale result" {
		t.Fatal("stale response was applied despite aiAnalysisFor having moved to a different row")
	}
	if !next.aiAnalyzing {
		t.Fatal("aiAnalyzing flipped to false from a stale response, want it to stay true for the still-in-flight newer request")
	}
}

func TestAKeyDispatchesAIAnalysisInProblemsPane(t *testing.T) {
	model := testModel()
	model.width, model.height = 100, 30
	model.focus = paneActivity
	model.mode = activityProblems
	model.settings.AIProvider = aiProviderAnthropic
	model.settings.AIAPIKey = "sk-test"

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	next := updated.(Model)
	if !next.aiAnalyzing {
		t.Fatal("aiAnalyzing = false after pressing \"a\" in the Problems pane, want true")
	}
	if cmd == nil {
		t.Fatal("\"a\" in the Problems pane returned nil cmd, want the analyze command")
	}
}

// TestAKeyInLogsModeDoesNotTriggerAIAnalysis guards the pre-existing "a"
// behavior in the Logs pane (severity filter) against this session's
// addition — the two must stay mode-gated and not collide.
func TestAKeyInLogsModeDoesNotTriggerAIAnalysis(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.width, model.height = 100, 30
	model.focus = paneActivity
	model.mode = activityLogs

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	next := updated.(Model)
	if next.aiAnalyzing {
		t.Fatal("aiAnalyzing = true after pressing \"a\" in the Logs pane, want false (severity filter, not AI)")
	}
	if next.logLevel != logSeverityAll {
		t.Fatalf("logLevel = %v after \"a\" in Logs mode, want logSeverityAll (existing behavior)", next.logLevel)
	}
}

// TestCopyRowsIncludesAIAnalysisForMatchingContainer checks the Copy
// overlay picks up a finished AI analysis as a selectable, copyable row —
// the feature requested live: copy AI analysis text out via the same Copy
// overlay every other container detail already goes through.
func TestCopyRowsIncludesAIAnalysisForMatchingContainer(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.aiAnalysisFor = model.selectedID
	model.aiAnalysis = "restart the postgres service"

	rows := model.copyRows()
	found := false
	for _, row := range rows {
		if row.label == "AI analysis" {
			found = true
			if row.value != "restart the postgres service" {
				t.Fatalf("AI analysis row value = %q, want the stored analysis text", row.value)
			}
		}
	}
	if !found {
		t.Fatal("copyRows() missing an \"AI analysis\" row despite aiAnalysisFor matching the selected container")
	}
}

// TestCopyRowsOmitsAIAnalysisForDifferentContainer guards the mismatch
// case: an AI result belonging to some other container (aiAnalysisFor
// doesn't match whatever's currently selected for Copy) must never show up
// misattributed to the wrong container.
func TestCopyRowsOmitsAIAnalysisForDifferentContainer(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.aiAnalysisFor = domain.ResourceID{Host: "local", ID: "some-other-container"}
	model.aiAnalysis = "advice for a different container"

	for _, row := range model.copyRows() {
		if row.label == "AI analysis" {
			t.Fatalf("copyRows() included AI analysis for a non-matching container: %#v", row)
		}
	}
}

// TestCopyRowsOmitsAIAnalysisWhenEmpty checks the row doesn't appear while
// analyzing/erroring (aiAnalysis empty) even if aiAnalysisFor matches —
// nothing to copy yet.
func TestCopyRowsOmitsAIAnalysisWhenEmpty(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.aiAnalysisFor = model.selectedID
	model.aiAnalysis = ""
	model.aiAnalyzing = true

	for _, row := range model.copyRows() {
		if row.label == "AI analysis" {
			t.Fatalf("copyRows() included an AI analysis row with no text yet: %#v", row)
		}
	}
}

// TestCopyOverlayCopiesAIAnalysisText is an end-to-end check that
// selecting the AI analysis row and confirming actually copies its text
// via the same OSC52 path every other Copy row already uses.
func TestCopyOverlayCopiesAIAnalysisText(t *testing.T) {
	model := testModelWithSelectedContainer()
	model.aiAnalysisFor = model.selectedID
	model.aiAnalysis = "check the logs for a crash loop"
	model.overlay = overlayCopy
	rows := model.copyRows()
	for i, row := range rows {
		if row.label == "AI analysis" {
			model.copyCursor = i
			break
		}
	}
	var out bytes.Buffer
	original := clipboardWriter
	clipboardWriter = &out
	defer func() { clipboardWriter = original }()

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.overlay != overlayNone {
		t.Fatalf("overlay = %v, want none after copying", model.overlay)
	}
	runCmd(t, cmd)
	if !strings.Contains(out.String(), "\x1b]52;c;") {
		t.Fatalf("clipboard output = %q, want an OSC52 sequence", out.String())
	}
}

// TestRenderProblemInsightShowsAIStateOnlyForMatchingRow checks
// renderProblemInsight's guard directly: an AI result only displays under
// the row it actually belongs to, never a different one just because it
// happens to be selected now.
func TestRenderProblemInsightShowsAIStateOnlyForMatchingRow(t *testing.T) {
	model := testModel()
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	rowA := problemRow{id: domain.ResourceID{Host: "local", ID: "1"}, name: "a", container: domain.Container{Health: domain.HealthUnhealthy}}
	rowB := problemRow{id: domain.ResourceID{Host: "local", ID: "2"}, name: "b", container: domain.Container{State: domain.StateExited}}

	model.aiAnalysisFor = rowA.id
	model.aiAnalysis = "ai says restart it"

	if got := ansi.Strip(model.renderProblemInsight(renderer, rowA, 60)); !strings.Contains(got, "ai says restart it") {
		t.Fatalf("renderProblemInsight(rowA) = %q, want the AI result", got)
	}
	if got := ansi.Strip(model.renderProblemInsight(renderer, rowB, 60)); strings.Contains(got, "ai says restart it") {
		t.Fatalf("renderProblemInsight(rowB) = %q, want the rule-based insight, not rowA's AI result", got)
	}
}

// TestRenderProblemInsightColorsAIHeadingDistinctly checks the feature
// requested live: the AI result gets a colored heading distinguishing it
// from the plain rule-based text, and an error gets its own distinct color
// — not the same code path, not indistinguishable from the accent color.
func TestRenderProblemInsightColorsAIHeadingDistinctly(t *testing.T) {
	model := testModel()
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})
	row := problemRow{id: domain.ResourceID{Host: "local", ID: "1"}, name: "a", container: domain.Container{Health: domain.HealthUnhealthy}}

	model.aiAnalysisFor = row.id
	model.aiAnalysis = "restart it"
	resultRendered := model.renderProblemInsight(renderer, row, 60)
	if resultRendered == ansi.Strip(resultRendered) {
		t.Fatal("AI result rendering carries no ANSI color codes at all, want a colored heading")
	}
	if !strings.Contains(ansi.Strip(resultRendered), "AI Analysis") {
		t.Fatalf("AI result rendering missing the \"AI Analysis\" heading:\n%s", ansi.Strip(resultRendered))
	}

	model.aiAnalysis = ""
	model.aiAnalysisErr = errBoom
	errRendered := model.renderProblemInsight(renderer, row, 60)
	if errRendered == ansi.Strip(errRendered) {
		t.Fatal("AI error rendering carries no ANSI color codes at all, want a colored heading")
	}
	if !strings.Contains(ansi.Strip(errRendered), "AI analysis failed") {
		t.Fatalf("AI error rendering missing the \"AI analysis failed\" heading:\n%s", ansi.Strip(errRendered))
	}
}

// TestRenderProblemInsightDoesNotTruncateTypicalAIResponse is the
// regression test for the bug reported live: a normal multi-sentence AI
// response (matching the prompt's own "3-6 sentences" instruction) was
// getting cut off by the old fixed 6-line insight budget. It must now
// render in full, no trailing ellipsis, once problemsInsightRows grows for
// active AI content.
func TestRenderProblemInsightDoesNotTruncateTypicalAIResponse(t *testing.T) {
	model := testModel()
	model.width, model.height = 120, 34
	model.focus = paneActivity
	model.mode = activityProblems
	model.problemCursor = 0
	renderer := tideui.NewRenderer(whatthedockTheme(), tideui.StyleOptions{Density: tideui.Compact, PaneCorners: tideui.RoundCorners})

	row := *model.currentProblem(model.snapshotProblems())
	model.aiAnalysisFor = row.id
	model.aiAnalysis = "This container is unhealthy because its health check keeps failing. " +
		"The most common cause is the application not being ready when the check runs, or the check " +
		"command itself pointing at the wrong port. Check the health check command's exact definition " +
		"and try running it manually inside the container. If it depends on another service (a database, " +
		"for instance), confirm that service is actually reachable from here. Once you find the real " +
		"failure, either fix the underlying issue or loosen the health check's timing so it isn't so quick to flag."

	width := model.centerPaneWidth() - 4
	got := ansi.Strip(model.renderProblemInsight(renderer, row, width))
	if strings.Contains(got, "…") {
		t.Fatalf("AI response was truncated despite being a normal length:\n%s", got)
	}
	if !strings.Contains(got, "flag.") {
		t.Fatalf("AI response missing its final word, want the full text intact:\n%s", got)
	}
}

var errBoom = &testBoomError{}

type testBoomError struct{}

func (e *testBoomError) Error() string { return "boom" }
