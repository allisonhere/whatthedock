package ui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/allisonhere/ripple"
	"github.com/allisonhere/tideui"
	osc52 "github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/whatthedock/internal/actions"
	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/domain"
	"github.com/allisonhere/whatthedock/internal/systems"
	"github.com/allisonhere/whatthedock/internal/update"
)

type pane int

const (
	paneTree pane = iota
	paneActivity
	paneInspector
)

type activityMode int

const (
	activityLogs activityMode = iota
	activityProblems
	activityStats
)

type overlayMode int

const (
	overlayNone overlayMode = iota
	overlayHelp
	overlayFilter
	overlayLogFilter
	overlayCommandPalette
	overlayThemePicker
	overlaySettings
	overlayCopy
	overlayOpen
	overlaySystems
	overlayCreate
	overlayAbout
	overlayDelete
	overlayReplicate
	overlayUpdate
	overlayAppLog
	overlayDashboard
)

type graphStyle int

const (
	graphStyleWave graphStyle = iota
	graphStyleBlocks
	graphStyleBraille
	// graphStyleBars is graphStyleBlocks' same 8-level glyph set with a
	// blank column after every bar (see graphGlyphSpacing) — a spaced-out
	// bar-chart look, distinct from blocks' unbroken sparkline.
	graphStyleBars
	// graphStyleGauge renders the latest value as a single proportional
	// fill bar (renderGaugeBar) instead of a glyph-per-sample history —
	// it doesn't use graphGlyphs for its main rendering path, only as a
	// fallback for the static/no-history cases every style still shares.
	graphStyleGauge
)

type graphColorMode int

const (
	graphColorGradient graphColorMode = iota
	graphColorMetric
	graphColorMono
)

type logColorMode int

const (
	logColorFull logColorMode = iota
	logColorSeverity
	logColorHTTP
	logColorMono
)

// appLogMode controls how much of whatthedock's own internal status-bar
// activity gets recorded into an in-memory buffer viewable via Settings >
// View app log. Error status lines are always kept regardless of this
// setting (see recordAppLog) — off just means routine info-level status
// messages aren't also kept. on adds those info lines, buffer only, for
// this session. save does the same but also appends every kept line to a
// log file on disk (see appLogFilePath), so it survives past the session,
// e.g. for reporting a bug that already scrolled off screen.
type appLogMode int

const (
	appLogOff appLogMode = iota
	appLogOn
	appLogSave
)

// aiProvider identifies which AI service the Problems pane's "analyze with
// AI" action calls (see internal/ai.Provider, which this maps directly
// onto). Custom is a user-supplied OpenAI-compatible base URL, covering
// anything else (a local Ollama server, OpenRouter, Azure OpenAI, ...)
// without a hardcoded name for each one.
type aiProvider int

const (
	aiProviderAnthropic aiProvider = iota
	aiProviderOpenAI
	aiProviderGemini
	aiProviderCustom
)

type logSeverityFilter int

const (
	logSeverityAll logSeverityFilter = iota
	logSeverityErrors
	logSeverityWarnings
	logSeverityInfo
)

type logViewState struct {
	filter     string
	level      logSeverityFilter
	scroll     int
	follow     bool
	matchIndex int
}

type appSettings struct {
	GraphStyle       graphStyle
	GraphColor       graphColorMode
	LogColor         logColorMode
	LogHealthColor   bool
	ShowDeltas       bool
	CreateVim        bool
	ModalShadow      bool
	StatsRefresh     time.Duration
	DefaultActivity  activityMode
	StartInDashboard bool
	AppLog           appLogMode

	// AI* configure the Problems pane's opt-in "analyze with AI" action —
	// see internal/ai and aiConfig. AIAPIKey is whatthedock's first
	// persisted secret; config.SaveSettings writes 0600 because of it.
	AIProvider aiProvider
	AIModel    string
	AIAPIKey   string
	AIBaseURL  string
}

type rowKind int

const (
	rowProject rowKind = iota
	rowService
	rowContainer
	rowSection
)

type treeRow struct {
	kind      rowKind
	label     string
	project   string
	service   string
	container *domain.Container
	depth     int
	muted     bool
}

type treeRowKey struct {
	valid       bool
	kind        rowKind
	label       string
	project     string
	service     string
	containerID domain.ResourceID
}

type settingsRow struct {
	label  string
	value  string
	kind   settingsRowKind
	action settingsAction
}

type copyRow struct {
	label string
	value string
}

type openKind int

const (
	openKindPort openKind = iota
	openKindMount
)

type openRow struct {
	kind   openKind
	label  string
	value  string
	target string
}

type settingsRowKind int

const (
	settingsRowSetting settingsRowKind = iota
	settingsRowSection
	settingsRowAction
	// settingsRowText and settingsRowSecretText are free-text rows — every
	// other settingsRowSetting row cycles through a fixed set of values
	// (see cycleSetting), but AI model/base URL/API key need arbitrary
	// typed text instead. Selecting one starts inline text editing (see
	// settingsEditingField) rather than cycling. settingsRowSecretText
	// additionally renders its value masked, never as plaintext.
	settingsRowText
	settingsRowSecretText
)

type settingsAction int

const (
	settingsActionNone settingsAction = iota
	settingsActionResetDefaults
	settingsActionCheckUpdate
	settingsActionViewAppLog
)

type systemOverlayMode int

const (
	systemModeList systemOverlayMode = iota
	systemModeEdit
	systemModeDelete
)

type systemField int

const (
	systemFieldName systemField = iota
	systemFieldKind
	systemFieldDockerHost
	systemFieldSSHHost
	systemFieldSSHUser
	systemFieldSSHPort
	systemFieldSSHAuth
	systemFieldRemoteSocket
	systemFieldLocalSocket
)

type providerFactory func(context.Context, config.System) (app.Provider, error)

type Model struct {
	provider app.Provider
	theme    tideui.Theme
	themes   tideui.ThemePicker

	width  int
	height int
	focus  pane
	mode   activityMode

	loading         bool
	snapshot        domain.Snapshot
	rows            []treeRow
	cursor          int
	focusedTreeKey  treeRowKey
	problemCursor   int
	collapsed       map[string]bool
	selectedID      domain.ResourceID
	selected        *domain.Container
	filter          string
	filterDraft     string
	inspectorScroll int
	helpScroll      int

	logLines          []string
	logFilter         string
	logDraft          string
	logLevel          logSeverityFilter
	logScroll         int
	logFollow         bool
	logMatch          int
	logViews          map[domain.ResourceID]logViewState
	logViewID         domain.ResourceID
	logChan           chan string
	logCancel         context.CancelFunc
	logLoading        bool
	logErr            error
	logReplaceOnDrain bool

	eventChan     <-chan domain.ContainerEvent
	eventCancel   context.CancelFunc
	snapshotDirty bool
	eventBackoff  time.Duration

	stats        *domain.ContainerStats
	statsID      domain.ResourceID
	statsHistory map[domain.ResourceID]statsHistory
	statsLoading bool
	statsErr     error

	status    string
	statusErr bool
	overlay   overlayMode

	// dashboardCursor is the currently highlighted row index within the
	// Dashboard overlay's own visible container list (see
	// dashboardBodyPlan) — reset to 0 each time the overlay opens.
	dashboardCursor int

	// appLogLines is every distinct status-bar message this session, kept
	// when settings.AppLog is on or save (see recordAppLog) — viewable via
	// Settings > View app log (overlayAppLog). appLogFile is the lazily
	// opened handle used only in save mode; appLogScroll is that overlay's
	// own scroll position, same role as helpScroll for the Help overlay.
	appLogLines  []string
	appLogFile   *os.File
	appLogScroll int

	// lastStatusErrText/statusErrSince back the minimum-hold guard in the
	// snapshotMsg handler: an error status must stay legible for at least
	// statusErrMinHold, not just until the next routine refresh happens to
	// land, but it also must not stick around forever once that window has
	// passed — see the snapshotMsg case for how these get used.
	lastStatusErrText string
	statusErrSince    time.Time

	commandFilter string
	commandCursor int

	settings       appSettings
	settingsDraft  appSettings
	settingsCursor int
	settingsPath   string

	// settingsEditingField is "" when no settings row is being text-edited,
	// otherwise the label of the settingsRowText/settingsRowSecretText row
	// currently open for inline editing (AI model/API key/base URL — the
	// only free-text rows in Settings). settingsEditDraft/settingsEditCursor
	// back that in-progress edit the same way createCursor does for the
	// Create form's text fields.
	settingsEditingField string
	settingsEditDraft    string
	settingsEditCursor   int
	systems              []config.System
	activeSystem         string
	systemsCursor        int
	systemMode           systemOverlayMode
	systemDraft          config.System
	systemDraftNew       bool
	systemField          systemField
	systemCursor         int
	providerFor          providerFactory

	copyCursor int
	openCursor int

	createDraft  createDraft
	createField  createField
	createCursor int

	createBrowsing    bool
	createBrowseDir   string
	createFiles       []createFileEntry
	createFileCursor  int
	createFileErr     string
	createFileLoading bool

	createEditingCompose bool
	createEditor         editorArea

	aboutFrame      int
	aboutSpotlights []aboutSpotlight

	// statusPulseFrame drives the breathing green dot shown in the status
	// bar in place of the "Docker connected" text once a system is up —
	// ticks continuously for the life of the program (see tickStatusPulse),
	// not scoped to any overlay.
	statusPulseFrame int

	// busy and replicateProgress drive the status-bar spinner. busy is set
	// the moment a long-running action (compose apply/delete/replicate,
	// standalone delete/replicate) dispatches and cleared when
	// actionDoneMsg/createDoneMsg lands. replicateProgress carries real
	// per-layer pull text for the one path with structured progress
	// (standalone Replicate) — nil for every other busy action, which show
	// the spinner with a static phase label only.
	busy              bool
	replicateProgress chan string

	// eventsReconnecting mirrors the event-stream backoff loop so statusLeft
	// can show it's happening instead of going silent for up to 30s.
	eventsReconnecting bool

	// appVersion is this build's version (set via WithVersion — a "dev"
	// build never has anything to compare against, see update.IsNewer).
	// updateIgnoredVersion/updateLastCheck mirror config.Settings'
	// UpdateIgnoredVersion/UpdateLastCheck (see persistedSettings) rather
	// than living in appSettings/settingsDraft: they're app state that
	// happens to persist, not a user-editable preference with a settings
	// row of its own the way GraphStyle etc. are. updateAvailableVersion
	// is non-empty while the "update available" overlay (or its result) is
	// live; restartExecPath is set once an install finishes — see
	// RestartExecPath in update.go for why main(), not this package, acts
	// on it.
	appVersion             string
	updateIgnoredVersion   string
	updateLastCheck        time.Time
	updateChecking         bool
	updateAvailableVersion string
	updateCheckErr         error
	updateInstalling       bool
	restartExecPath        string

	// aiAnalyzing/aiAnalysis/aiAnalysisErr mirror the update-check pattern
	// above (updateChecking/updateCheckErr) for the Problems pane's "a"
	// (analyze with AI) action. aiAnalysisFor is the problem row's ID the
	// current result/error/in-flight request actually belongs to — checked
	// against whatever row is currently selected before ever rendering
	// aiAnalysis/aiAnalysisErr, so moving the cursor to a different problem
	// while a request is in flight (or after one finished) never shows a
	// stale result attributed to the wrong container.
	aiAnalyzing   bool
	aiAnalysis    string
	aiAnalysisErr error
	aiAnalysisFor domain.ResourceID
}

// aboutSpotlight is one moving light in the About screen's animation
// (a port of terminaltexteffects' "spotlights" effect): it wanders to
// random points within the logo grid during the search phase, illuminating
// whatever glyphs it passes near, then converges on the grid's center once
// the search window ends so the reveal can expand outward from there.
type aboutSpotlight struct {
	row, col             float64 // current position, in grid coordinates
	targetRow, targetCol float64
	speed                float64 // grid cells covered per frame
}

const (
	aboutSpotlightCount = 3
	// aboutSearchFrames, aboutConvergeFrames, and aboutExpandFrames are the
	// three phases in sequence: wander randomly, snap targets to center and
	// converge, then grow a reveal radius from center until every glyph is
	// lit. Once frame passes the sum of all three the render is a stable,
	// fully-lit final state — no separate "done" flag needed.
	aboutSearchFrames   = 70
	aboutConvergeFrames = 20
	aboutExpandFrames   = 18
	// aboutBeamRadius and aboutBeamFalloff shape each spotlight's beam: a
	// hard-lit core out to radius*(1-falloff), then a soft linear fade to
	// unlit at the full radius, rather than a hard-edged circle.
	aboutBeamRadius  = 2.4
	aboutBeamFalloff = 0.6
)

// newAboutSpotlights spawns count spotlights at random positions within a
// rows x cols grid, each already wandering toward its own random target.
func newAboutSpotlights(count, rows, cols int) []aboutSpotlight {
	spotlights := make([]aboutSpotlight, count)
	for i := range spotlights {
		row, col := randomAboutTarget(rows, cols)
		targetRow, targetCol := randomAboutTarget(rows, cols)
		spotlights[i] = aboutSpotlight{
			row: row, col: col,
			targetRow: targetRow, targetCol: targetCol,
			speed: 0.35 + rand.Float64()*0.4,
		}
	}
	return spotlights
}

func randomAboutTarget(rows, cols int) (float64, float64) {
	if rows <= 1 {
		rows = 2
	}
	if cols <= 1 {
		cols = 2
	}
	return rand.Float64() * float64(rows-1), rand.Float64() * float64(cols-1)
}

// tickAboutSpotlights advances the About screen's animation one frame.
// During the search window each spotlight wanders toward its own random
// target, picking a new one on arrival; once the window ends, every
// spotlight's target snaps to the grid center so they visibly converge —
// matching the reference effect's "search, then converge" behavior. The
// expand phase that follows needs no per-spotlight state; it's computed
// purely from frame count in spotlightRowCells.
func (m Model) tickAboutSpotlights() Model {
	rows := len(aboutLogo())
	cols := aboutContentWidth(m.width)
	if cols <= 0 {
		return m
	}
	converging := m.aboutFrame >= aboutSearchFrames
	centerRow, centerCol := float64(rows-1)/2, float64(cols-1)/2

	for i := range m.aboutSpotlights {
		sp := &m.aboutSpotlights[i]
		if converging {
			sp.targetRow, sp.targetCol = centerRow, centerCol
		}
		dRow, dCol := sp.targetRow-sp.row, sp.targetCol-sp.col
		dist := math.Hypot(dRow, dCol)
		if dist <= sp.speed {
			sp.row, sp.col = sp.targetRow, sp.targetCol
			if !converging {
				sp.targetRow, sp.targetCol = randomAboutTarget(rows, cols)
			}
			continue
		}
		sp.row += dRow / dist * sp.speed
		sp.col += dCol / dist * sp.speed
	}
	return m
}

var clipboardWriter io.Writer = os.Stderr
var openTarget = defaultOpenTarget
var applyComposeCreate = defaultApplyComposeCreate
var applyComposeAdopt = defaultApplyComposeAdopt
var applyComposeDelete = defaultApplyComposeDelete
var applyComposeReplicate = defaultApplyComposeReplicate
var composeCommand = runDockerCompose

type snapshotMsg struct {
	snapshot domain.Snapshot
	err      error
}

type detailMsg struct {
	id        domain.ResourceID
	container domain.Container
	err       error
}

type statsMsg struct {
	stats domain.ContainerStats
	err   error
}

type statsTickMsg struct {
	id domain.ResourceID
}

// dashboardStatsMsg/dashboardTickMsg drive the Dashboard overlay's own
// stats-polling loop — deliberately separate from statsMsg/statsTickMsg's
// single-selected-container loop above (see dashboardRefreshCmd) rather
// than reusing it, so there's no risk of the two interacting and
// regressing the already-fixed "stats loading flash" bug that loop's
// gating logic depends on.
type dashboardStatsMsg struct {
	stats domain.ContainerStats
	err   error // one container's fetch failing shouldn't blank the rest
}

type dashboardTickMsg struct{}

type statsHistory struct {
	CPU        []float64
	Memory     []uint64
	NetworkRx  []uint64
	NetworkTx  []uint64
	BlockTotal []uint64
	PIDs       []uint64
	maxCPU     float64
	maxMemory  uint64
	maxNetwork uint64
	maxBlock   uint64
	maxPIDs    uint64
	lastStats  *domain.ContainerStats
}

type actionDoneMsg struct {
	label string
	err   error
}

type logsStartedMsg struct {
	id     domain.ResourceID
	lines  <-chan string
	cancel context.CancelFunc
	err    error
}

type eventsStartedMsg struct {
	events <-chan domain.ContainerEvent
	cancel context.CancelFunc
	err    error
}

type containerEventMsg struct {
	event domain.ContainerEvent
}

type eventStreamClosedMsg struct{}

type eventsReconnectTickMsg struct{}

type eventRefreshTickMsg struct{}

type logTickMsg struct{}

type aboutTickMsg struct{}

type statusPulseTickMsg struct{}

type openDoneMsg struct {
	label string
	err   error
}

type systemSwitchMsg struct {
	system   config.System
	provider app.Provider
	err      error
}

type systemTunnelMsg struct {
	system config.System
	test   bool
	err    error
}

// execShellDoneMsg carries the result of an exec-shell session back into
// Update once the terminal handoff (tea.ExecProcess) returns control to the
// TUI.
type execShellDoneMsg struct {
	name string
	err  error
}

type systemTestMsg struct {
	system config.System
	err    error
}

type createDoneMsg struct {
	name   string
	id     domain.ResourceID
	edited bool
	err    error
}

func NewModel(provider app.Provider) Model {
	return NewModelWithSettings(provider, config.Settings{}, "")
}

func NewModelWithSettings(provider app.Provider, persisted config.Settings, settingsPath string) Model {
	return NewModelWithProviderFactory(provider, persisted, settingsPath, nil)
}

func NewModelWithProviderFactory(provider app.Provider, persisted config.Settings, settingsPath string, factory providerFactory) Model {
	theme := whatthedockTheme()
	themes := append([]tideui.Theme{theme}, tideui.BuiltinThemes...)
	initialThemeName := theme.Name
	if persisted.Theme != "" {
		initialThemeName = persisted.Theme
	}
	themePicker := tideui.NewThemePicker(tideui.ThemePickerOptions{Themes: themes, InitialTheme: initialThemeName, Title: "THEMES"})
	theme = themePicker.ConfirmedTheme()
	settings := defaultSettings()
	settings.applyPersisted(persisted)
	setEditorVimMode(settings.CreateVim)
	persisted = config.NormalizeSystems(persisted)
	var updateLastCheck time.Time
	if persisted.UpdateLastCheck != "" {
		updateLastCheck, _ = time.Parse(time.RFC3339, persisted.UpdateLastCheck)
	}
	m := Model{
		provider:             provider,
		theme:                theme,
		themes:               themePicker,
		mode:                 settings.DefaultActivity,
		settings:             settings,
		settingsDraft:        settings,
		settingsPath:         settingsPath,
		systems:              persisted.Systems,
		activeSystem:         persisted.ActiveSystem,
		providerFor:          factory,
		statsHistory:         map[domain.ResourceID]statsHistory{},
		logViews:             map[domain.ResourceID]logViewState{},
		logFollow:            true,
		collapsed:            map[string]bool{},
		loading:              true,
		status:               "connecting to Docker",
		appVersion:           "dev",
		updateIgnoredVersion: persisted.UpdateIgnoredVersion,
		updateLastCheck:      updateLastCheck,
	}
	if settings.StartInDashboard {
		m.overlay = overlayDashboard
	}
	return m
}

// WithVersion sets the running build's version (from cmd/whatthedock's
// ldflags-injected main.version) — a separate step from construction so
// every other caller (tests, the demo path) doesn't have to thread a
// version string through just to get "dev", the correct default for a
// non-release build (see update.IsNewer).
func (m Model) WithVersion(version string) Model {
	m.appVersion = version
	return m
}

func defaultSettings() appSettings {
	return appSettings{
		GraphStyle:      graphStyleWave,
		GraphColor:      graphColorGradient,
		LogColor:        logColorFull,
		LogHealthColor:  true,
		ShowDeltas:      true,
		ModalShadow:     true,
		StatsRefresh:    2 * time.Second,
		DefaultActivity: activityProblems,
	}
}

func (s *appSettings) applyPersisted(persisted config.Settings) {
	switch persisted.GraphStyle {
	case "blocks":
		s.GraphStyle = graphStyleBlocks
	case "braille":
		s.GraphStyle = graphStyleBraille
	case "bars":
		s.GraphStyle = graphStyleBars
	case "gauge":
		s.GraphStyle = graphStyleGauge
	case "wave", "":
		s.GraphStyle = graphStyleWave
	}
	switch persisted.GraphColor {
	case "metric":
		s.GraphColor = graphColorMetric
	case "mono":
		s.GraphColor = graphColorMono
	case "gradient", "":
		s.GraphColor = graphColorGradient
	}
	switch persisted.LogColor {
	case "severity":
		s.LogColor = logColorSeverity
	case "http":
		s.LogColor = logColorHTTP
	case "mono":
		s.LogColor = logColorMono
	case "full", "":
		s.LogColor = logColorFull
	}
	if persisted.LogHealthColor != nil {
		s.LogHealthColor = *persisted.LogHealthColor
	}
	if persisted.ShowDeltas != nil {
		s.ShowDeltas = *persisted.ShowDeltas
	}
	if persisted.CreateVim != nil {
		s.CreateVim = *persisted.CreateVim
	}
	if persisted.ModalShadow != nil {
		s.ModalShadow = *persisted.ModalShadow
	}
	if persisted.StatsRefresh != "" {
		if interval, err := time.ParseDuration(persisted.StatsRefresh); err == nil && interval > 0 {
			s.StatsRefresh = interval
		}
	}
	switch persisted.DefaultActivity {
	case "logs":
		s.DefaultActivity = activityLogs
	case "stats":
		s.DefaultActivity = activityStats
	case "problems", "":
		s.DefaultActivity = activityProblems
	}
	if persisted.StartInDashboard != nil {
		s.StartInDashboard = *persisted.StartInDashboard
	}
	switch persisted.AppLog {
	case "on":
		s.AppLog = appLogOn
	case "save":
		s.AppLog = appLogSave
	case "off", "":
		s.AppLog = appLogOff
	}
	switch persisted.AIProvider {
	case "openai":
		s.AIProvider = aiProviderOpenAI
	case "gemini":
		s.AIProvider = aiProviderGemini
	case "custom":
		s.AIProvider = aiProviderCustom
	case "anthropic", "":
		s.AIProvider = aiProviderAnthropic
	}
	s.AIModel = persisted.AIModel
	s.AIAPIKey = persisted.AIAPIKey
	s.AIBaseURL = persisted.AIBaseURL
}

func (s appSettings) persisted() config.Settings {
	showDeltas := s.ShowDeltas
	logHealthColor := s.LogHealthColor
	createVim := s.CreateVim
	modalShadow := s.ModalShadow
	startInDashboard := s.StartInDashboard
	return config.Settings{
		GraphStyle:       s.GraphStyle.String(),
		GraphColor:       s.GraphColor.String(),
		LogColor:         s.LogColor.String(),
		LogHealthColor:   &logHealthColor,
		ShowDeltas:       &showDeltas,
		CreateVim:        &createVim,
		ModalShadow:      &modalShadow,
		StatsRefresh:     formatRefreshInterval(s.StatsRefresh),
		DefaultActivity:  activityModeName(s.DefaultActivity),
		StartInDashboard: &startInDashboard,
		AppLog:           s.AppLog.String(),
		AIProvider:       s.AIProvider.String(),
		AIModel:          s.AIModel,
		AIAPIKey:         s.AIAPIKey,
		AIBaseURL:        s.AIBaseURL,
	}
}

func (m Model) persistedSettings() config.Settings {
	settings := m.settings.persisted()
	settings.Theme = m.theme.Name
	settings.Systems = append([]config.System(nil), m.systems...)
	settings.ActiveSystem = m.activeSystem
	settings.UpdateIgnoredVersion = m.updateIgnoredVersion
	if !m.updateLastCheck.IsZero() {
		settings.UpdateLastCheck = m.updateLastCheck.Format(time.RFC3339)
	}
	return config.NormalizeSystems(settings)
}

func whatthedockTheme() tideui.Theme {
	return tideui.Theme{
		Name:          "whatthedock",
		Bg:            "#101419",
		Fg:            "#e8edf2",
		Border:        "#333c46",
		BorderFocus:   "#7dcfff",
		Selected:      "#26313a",
		Unread:        "#80c990",
		Dimmed:        "#9aa6b2",
		StatusBar:     "#1e242b",
		StatusFg:      "#e8edf2",
		Error:         "#e06c75",
		Overlay:       "#171c22",
		OverlayBorder: "#7dcfff",
	}
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.refreshCmd(), m.startEventsCmd(), tickStatusPulse()}
	if cmd := m.autoCheckForUpdateCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	// Settings > Start in dashboard opens the Dashboard overlay from the
	// very first frame (see NewModelWithProviderFactory), but that alone
	// doesn't start its stats-polling loop — dashboardRefreshCmd is the
	// same command the 'd' key dispatches, and it's already a no-op
	// (returns nil) unless m.overlay == overlayDashboard, so this is safe
	// to call unconditionally.
	if cmd := m.dashboardRefreshCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// Update is bubbletea's entry point — a thin wrapper around updateStep that
// also records the status-bar transition into the app log (see appLogMode),
// so every one of updateStep's ~70 existing m.status/m.statusErr call sites
// gets logged for free instead of needing to be touched individually.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	prevStatus, prevErr := m.status, m.statusErr
	next, cmd := m.updateStep(msg)
	if nm, ok := next.(Model); ok {
		nm.recordAppLog(prevStatus, prevErr)
		return nm, cmd
	}
	return next, cmd
}

func (m Model) updateStep(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case snapshotMsg:
		m.loading = false
		if msg.err != nil {
			m.status, m.statusErr = friendlyDockerError(msg.err), true
			m.snapshot = domain.Snapshot{Host: m.provider.Host(), Refreshed: time.Now()}
			m.rows = m.buildRows()
			m.focusedTreeKey = treeRowKey{}
			m.clearSelectedContainer()
			return m, nil
		}
		// A routine refresh (the 250ms-debounced re-snapshot after any
		// Docker event — see eventRefreshTickCmd) fires after almost every
		// action, including the action that just set an error status the
		// user hasn't had a chance to read yet. Hold an error on screen for
		// at least statusErrMinHold — long enough to actually read — instead
		// of letting the very next routine refresh stomp it; an explicit
		// action still overwrites m.status directly regardless of the hold,
		// so a genuinely new event (retrying, a different action) always
		// takes over immediately. Once the hold has passed, routine refreshes
		// resume clearing it on their own — an error that's simply gone
		// stale (the underlying problem was fixed some other way) doesn't
		// stay pinned forever with no explicit action to clear it.
		if m.statusErr && m.status != m.lastStatusErrText {
			m.lastStatusErrText = m.status
			m.statusErrSince = time.Now()
		}
		if !m.statusErr || time.Since(m.statusErrSince) >= statusErrMinHold {
			m.status, m.statusErr = "Docker connected", false
		}
		if !m.focusedTreeKey.valid {
			m.syncFocusedTreeKey()
		}
		previousCursor := m.cursor
		m.snapshot = msg.snapshot
		m.rows = m.buildRows()
		return m, m.restoreFocusedTreeRow(previousCursor, true)
	case detailMsg:
		if msg.id != m.selectedID {
			// A newer selection has since superseded this in-flight request; discard it.
			return m, nil
		}
		if msg.err != nil {
			m.status, m.statusErr = friendlyDockerError(msg.err), true
			return m, nil
		}
		m.saveLogViewState()
		selectionChanged := m.selected == nil || msg.container.ID != m.selected.ID
		m.selected = &msg.container
		if selectionChanged {
			if m.logCancel != nil {
				m.logCancel()
				m.logCancel = nil
			}
			m.logChan = nil
			m.logLines = nil
			m.logErr = nil
			m.logReplaceOnDrain = false
			m.inspectorScroll = 0
		}
		m.restoreLogViewState(msg.container.ID)
		if m.mode == activityStats {
			m.statsLoading = true
			m.statsErr = nil
			return m, m.loadStatsCmd(msg.container.ID)
		}
		if !selectionChanged && m.logChan != nil {
			return m, nil
		}
		return m, m.startLogsCmd(msg.container.ID)
	case logsStartedMsg:
		if msg.id != m.selectedID {
			if msg.cancel != nil {
				msg.cancel()
			}
			return m, nil
		}
		if m.logCancel != nil {
			m.logCancel()
		}
		if len(m.logLines) == 0 {
			m.logLines = nil
			m.logReplaceOnDrain = false
		} else {
			m.logReplaceOnDrain = true
		}
		m.logErr = msg.err
		m.logLoading = false
		if msg.err != nil {
			m.logReplaceOnDrain = false
			m.status, m.statusErr = friendlyDockerError(msg.err), true
			return m, nil
		}
		m.logChan = make(chan string, 256)
		m.logCancel = msg.cancel
		go forwardLogs(msg.lines, m.logChan)
		return m, tickLogs()
	case eventsStartedMsg:
		if msg.err != nil {
			m.eventsReconnecting = true
			m.advanceEventBackoff()
			return m, m.eventsReconnectCmd()
		}
		m.eventsReconnecting = false
		m.eventChan = msg.events
		m.eventCancel = msg.cancel
		m.eventBackoff = 0
		return m, waitForContainerEvent(m.eventChan)
	case containerEventMsg:
		alreadyDirty := m.snapshotDirty
		m.snapshotDirty = true
		if alreadyDirty {
			return m, waitForContainerEvent(m.eventChan)
		}
		return m, tea.Batch(waitForContainerEvent(m.eventChan), eventRefreshTickCmd())
	case eventStreamClosedMsg:
		m.eventChan = nil
		if m.eventCancel != nil {
			m.eventCancel()
			m.eventCancel = nil
		}
		m.eventsReconnecting = true
		m.advanceEventBackoff()
		return m, m.eventsReconnectCmd()
	case eventsReconnectTickMsg:
		return m, m.startEventsCmd()
	case eventRefreshTickMsg:
		if !m.snapshotDirty {
			return m, nil
		}
		m.snapshotDirty = false
		return m, m.refreshCmd()
	case logTickMsg:
		m.drainLogs()
		if m.logChan == nil {
			return m, nil
		}
		return m, tickLogs()
	case aboutTickMsg:
		if m.overlay != overlayAbout {
			return m, nil
		}
		m.aboutFrame++
		m = m.tickAboutSpotlights()
		return m, tickAbout()
	case statusPulseTickMsg:
		m.statusPulseFrame++
		m.drainReplicateProgress()
		return m, tickStatusPulse()
	case statsMsg:
		if msg.stats.ID != m.selectedID {
			return m, nil
		}
		m.statsLoading = false
		m.statsErr = msg.err
		if msg.err != nil {
			return m, m.nextStatsTickCmd(msg.stats.ID)
		}
		m.stats = &msg.stats
		m.statsID = msg.stats.ID
		m.appendStats(msg.stats)
		return m, m.nextStatsTickCmd(msg.stats.ID)
	case statsTickMsg:
		if m.mode != activityStats || msg.id != m.selectedID || m.statsLoading {
			return m, nil
		}
		// Deliberately does NOT re-arm statsLoading here: it should only be
		// true for the genuine first load of a newly selected container
		// (set directly wherever selection changes — see detailMsg,
		// actions.ShowStats, and "g"), never on a routine background poll.
		// A stats fetch that keeps failing leaves m.stats nil forever, so
		// re-arming it on every tick here used to flip the "loading
		// stats…" header on and off once per poll interval indefinitely —
		// reported live as text flashing too fast to read.
		return m, m.loadStatsCmd(msg.id)
	case dashboardStatsMsg:
		// A response for a screen the user already left (closed the
		// overlay since this was dispatched) — discard rather than
		// appending into history nobody's looking at anymore.
		if m.overlay != overlayDashboard {
			return m, nil
		}
		if msg.err == nil {
			m.appendStats(msg.stats)
		}
		return m, nil
	case dashboardTickMsg:
		if m.overlay != overlayDashboard {
			return m, nil
		}
		return m, m.dashboardRefreshCmd()
	case actionDoneMsg:
		m.busy = false
		m.replicateProgress = nil
		if msg.err != nil {
			m.status, m.statusErr = msg.label+": "+friendlyDockerError(msg.err), true
		} else {
			m.status, m.statusErr = msg.label+" complete", false
		}
		return m, m.refreshCmd()
	case updateCheckMsg:
		m.updateChecking = false
		m.updateLastCheck = time.Now()
		m.saveSettings()
		if msg.err != nil {
			m.updateCheckErr = msg.err
			if msg.manual {
				m.status, m.statusErr = "check for update: "+friendlyDockerError(msg.err), true
			}
			return m, nil
		}
		m.updateCheckErr = nil
		if !update.IsNewer(m.appVersion, msg.latest) {
			if msg.manual {
				m.status, m.statusErr = "whatthedock "+m.appVersion+" is up to date", false
			}
			return m, nil
		}
		// The automatic background check stays quiet about a version the
		// user already said to ignore; a manual "Check for update" always
		// shows it — you asked, so it answers, regardless of any earlier
		// dismissal.
		if !msg.manual && msg.latest == m.updateIgnoredVersion {
			return m, nil
		}
		m.updateAvailableVersion = msg.latest
		if m.overlay == overlayNone {
			m.overlay = overlayUpdate
		} else {
			m.status, m.statusErr = "update "+msg.latest+" available (see Settings)", false
		}
		return m, nil
	case updateInstalledMsg:
		m.updateInstalling = false
		if msg.err != nil {
			m.status, m.statusErr = "update failed: "+friendlyDockerError(msg.err), true
			return m, nil
		}
		m.restartExecPath = msg.exePath
		return m, tea.Quit
	case aiAnalysisDoneMsg:
		if msg.id != m.aiAnalysisFor {
			// A response for a row the user has since moved past (a newer
			// "a" press advanced aiAnalysisFor) — discard rather than
			// showing it under whatever's selected now.
			return m, nil
		}
		m.aiAnalyzing = false
		if msg.err != nil {
			m.aiAnalysisErr = msg.err
			return m, nil
		}
		m.aiAnalysis = msg.result
		return m, nil
	case createDoneMsg:
		m.busy = false
		m.overlay = overlayNone
		verb := "create"
		if msg.edited {
			verb = "update"
		}
		if msg.err != nil {
			m.status, m.statusErr = verb+" "+msg.name+": "+friendlyDockerError(msg.err), true
			return m, nil
		}
		if msg.id.ID != "" {
			m.selectedID = msg.id
			m.focusedTreeKey = treeRowKey{valid: true, kind: rowContainer, containerID: msg.id}
		}
		if msg.edited {
			m.status, m.statusErr = "updated "+msg.name, false
		} else {
			m.status, m.statusErr = "created "+msg.name, false
		}
		return m, m.refreshCmd()
	case ripple.SubmitMsg:
		if m.createEditingCompose {
			m.saveCreateEditor()
		}
		return m, nil
	case ripple.CancelMsg:
		if m.createEditingCompose {
			m.cancelCreateEditor()
		}
		return m, nil
	case createOverrideCheckMsg:
		if m.overlay == overlayCreate && m.createDraft.Mode == createModeCompose && m.createDraft.Service == msg.service {
			m.createDraft.BaseFileMissing = msg.baseFileMissing
			if msg.found {
				m.createDraft.OverrideRaw = msg.content
				m.createDraft.OverrideRawSet = true
				m.createDraft.OverrideLoaded = true
				m.createDraft.applyOverrideFieldsFromYAML(msg.content)
				m.status, m.statusErr = "loaded existing override for "+msg.service, false
			}
		}
		return m, nil
	case createFileBrowseMsg:
		m.createFileLoading = false
		m.createFileCursor = 0
		if msg.err != nil {
			m.createFileErr = msg.err.Error()
			m.createFiles = nil
			return m, nil
		}
		m.createBrowseDir = msg.dir
		m.createFiles = msg.entries
		m.createFileErr = ""
		return m, nil
	case openDoneMsg:
		if msg.err != nil {
			m.status, m.statusErr = "open "+msg.label+": "+msg.err.Error(), true
		}
		return m, nil
	case execShellDoneMsg:
		// The user directly saw whatever docker printed on the real
		// terminal during the handoff, so a non-nil err here (which could
		// just as easily be the shell's own last-command exit status as a
		// genuine launch failure — the two look identical to cmd.Run())
		// is reported informationally, not as an alarming error.
		if msg.err != nil {
			m.status, m.statusErr = "shell in "+msg.name+" exited: "+msg.err.Error(), false
		} else {
			m.status, m.statusErr = "closed shell in "+msg.name, false
		}
		return m, m.refreshCmd()
	case systemSwitchMsg:
		if msg.err != nil {
			m.status, m.statusErr = "system: "+msg.err.Error(), true
			return m, nil
		}
		m.cleanup()
		m.provider = msg.provider
		m.activeSystem = msg.system.ID
		m.overlay = overlayNone
		m.resetDockerState()
		m.saveSettings()
		m.status, m.statusErr = "system: "+msg.system.Name, false
		return m, tea.Batch(m.refreshCmd(), m.startEventsCmd())
	case systemTunnelMsg:
		if msg.err != nil {
			m.status, m.statusErr = "system: "+msg.err.Error(), true
			return m, nil
		}
		if msg.test {
			m.status, m.statusErr = "SSH tunnel ready; testing "+msg.system.Name, false
			return m, m.providerTestCmd(msg.system)
		}
		m.status, m.statusErr = "SSH tunnel ready; switching to "+msg.system.Name, false
		return m, m.providerSwitchCmd(msg.system)
	case systemTestMsg:
		if msg.err != nil {
			m.status, m.statusErr = "test "+msg.system.Name+": "+friendlyDockerError(msg.err), true
			return m, nil
		}
		m.status, m.statusErr = "test "+msg.system.Name+": connected", false
		return m, nil
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		if m.overlay != overlayNone {
			return m.handleOverlayKey(msg)
		}
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.cleanup()
		return m, tea.Quit
	case "tab":
		m.focus = (m.focus + 1) % 3
	case "shift+tab":
		m.focus = (m.focus + 2) % 3
	case "j", "down":
		switch {
		case m.focus == paneActivity && m.mode == activityProblems:
			m.moveProblemCursor(1)
		case m.focus == paneActivity && m.mode == activityLogs:
			m.scrollLogs(1)
		case m.focus == paneInspector:
			m.scrollInspector(1)
		case m.focus == paneTree:
			return m, m.focusTreeIndex(m.cursor + 1)
		}
		return m, nil
	case "k", "up":
		switch {
		case m.focus == paneActivity && m.mode == activityProblems:
			m.moveProblemCursor(-1)
		case m.focus == paneActivity && m.mode == activityLogs:
			m.scrollLogs(-1)
		case m.focus == paneInspector:
			m.scrollInspector(-1)
		case m.focus == paneTree:
			return m, m.focusTreeIndex(m.cursor - 1)
		}
		return m, nil
	case "pgdown":
		if m.focus == paneActivity && m.mode == activityLogs {
			m.scrollLogs(max(1, m.logVisibleRows()-1))
			return m, nil
		}
	case "pgup":
		if m.focus == paneActivity && m.mode == activityLogs {
			m.scrollLogs(-max(1, m.logVisibleRows()-1))
			return m, nil
		}
	case "home":
		if m.focus == paneActivity && m.mode == activityLogs {
			m.logFollow = false
			m.logScroll = 0
			m.saveLogViewState()
			return m, nil
		}
	case "end", "f":
		if m.mode == activityLogs {
			m.focus = paneActivity
			m.followLogs()
			return m, nil
		}
	case "esc":
		if m.mode == activityLogs && (strings.TrimSpace(m.logFilter) != "" || m.logLevel != logSeverityAll) {
			m.clearLogFilter()
			return m, nil
		}
	case "x":
		if m.mode == activityLogs && (strings.TrimSpace(m.logFilter) != "" || m.logLevel != logSeverityAll) {
			m.focus = paneActivity
			m.clearLogFilter()
			return m, nil
		}
	case "n":
		if m.focus == paneActivity && m.mode == activityLogs {
			if strings.TrimSpace(m.logFilter) == "" {
				m.openLogFilter()
				return m, nil
			}
			m.jumpLogMatch(1)
			return m, nil
		}
		return m, m.openCreateOverlay()
	case "N":
		if m.mode == activityLogs && strings.TrimSpace(m.logFilter) != "" {
			m.focus = paneActivity
			m.jumpLogMatch(-1)
			return m, nil
		}
	case " ":
		if m.focus == paneTree {
			if row := m.currentRow(); row != nil && row.kind == rowProject {
				m.collapsed[row.project] = !m.collapsed[row.project]
				previousCursor := m.cursor
				m.rows = m.buildRows()
				return m, m.restoreFocusedTreeRow(previousCursor, false)
			}
		}
	case "enter":
		if m.focus == paneActivity && m.mode == activityProblems {
			return m.selectProblem(m.problemCursor)
		}
		if m.focus == paneTree {
			if row := m.currentRow(); row != nil {
				if row.kind == rowProject {
					m.collapsed[row.project] = !m.collapsed[row.project]
					previousCursor := m.cursor
					m.rows = m.buildRows()
					return m, m.restoreFocusedTreeRow(previousCursor, false)
				}
				if row.container != nil {
					return m, m.loadSelectedCmd()
				}
			}
		}
	case "/":
		if m.mode == activityLogs {
			m.focus = paneActivity
			m.openLogFilter()
			return m, nil
		}
		m.overlay = overlayFilter
		m.filterDraft = m.filter
	case "a":
		if m.focus == paneActivity && m.mode == activityLogs {
			m.setLogSeverityFilter(logSeverityAll)
			return m, nil
		}
		if m.focus == paneActivity && m.mode == activityProblems {
			return m.startAIAnalysis()
		}
	case "e":
		if m.focus == paneActivity && m.mode == activityLogs {
			m.setLogSeverityFilter(logSeverityErrors)
			return m, nil
		}
		if selected := m.selectedContainer(); selected != nil && selected.IsRunning() {
			return m, m.execShellCmd(*selected)
		}
	case "w":
		if m.focus == paneActivity && m.mode == activityLogs {
			m.setLogSeverityFilter(logSeverityWarnings)
			return m, nil
		}
	case "i":
		if m.focus == paneActivity && m.mode == activityLogs {
			m.setLogSeverityFilter(logSeverityInfo)
			return m, nil
		}
	case "?":
		m.overlay = overlayHelp
		m.helpScroll = 0
	case "A":
		return m.openAboutOverlay()
	case "d":
		return m.openDashboardOverlay()
	case "T":
		m.openThemePicker()
	case ",", "ctrl+,":
		m.openSettingsOverlay()
	case "S":
		m.openSystemsOverlay()
	case "ctrl+k":
		m.overlay = overlayCommandPalette
		m.commandFilter = ""
		m.commandCursor = 0
	case "c":
		m.openCopyOverlay()
	case "o":
		m.openOpenOverlay()
	case "r":
		if msg.Alt {
			return m, m.actionCmd(actions.Restart, "restart")
		}
		return m, m.refreshCmd()
	case "s":
		return m, m.actionCmd(actions.StartStop, "start/stop")
	case "l":
		m.focus = paneActivity
		m.mode = activityLogs
		if m.selected != nil {
			return m, m.startLogsCmd(m.selected.ID)
		}
	case "p":
		m.focus = paneActivity
		m.mode = activityProblems
		m.syncProblemCursor()
	case "g":
		m.focus = paneActivity
		m.mode = activityStats
		if selected := m.selectedContainer(); selected != nil {
			m.selectedID = selected.ID
			m.statsLoading = true
			m.statsErr = nil
			return m, m.loadStatsCmd(selected.ID)
		}
	case "D":
		if selected := m.selectedContainer(); selected != nil {
			m.overlay = overlayDelete
		}
	case "u":
		if selected := m.selectedContainer(); selected != nil {
			m.overlay = overlayReplicate
		}
	case "C":
		if selected := m.selectedContainer(); selected != nil {
			m.openCloneOverlay()
		}
	case "m":
		if selected := m.selectedContainer(); selected != nil {
			return m, m.openEditOverlay()
		}
	}
	return m, nil
}

func (m Model) handleOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.overlay {
	case overlayFilter:
		switch msg.String() {
		case "esc":
			m.overlay = overlayNone
			m.filterDraft = ""
		case "enter":
			m.filter = strings.TrimSpace(m.filterDraft)
			m.overlay = overlayNone
			previousCursor := m.cursor
			m.rows = m.buildRows()
			return m, m.restoreFocusedTreeRow(previousCursor, true)
		case "backspace":
			if len(m.filterDraft) > 0 {
				m.filterDraft = m.filterDraft[:len(m.filterDraft)-1]
			}
		default:
			if len(msg.Runes) > 0 {
				m.filterDraft += string(msg.Runes)
			}
		}
	case overlayLogFilter:
		switch msg.String() {
		case "esc":
			m.overlay = overlayNone
			m.logDraft = ""
		case "enter":
			m.logFilter = strings.TrimSpace(m.logDraft)
			m.logMatch = 0
			m.overlay = overlayNone
			m.clampLogScroll()
			m.saveLogViewState()
			m.status, m.statusErr = "log filter: "+filterStatus(m.logFilter, m.logLevel), false
		case "backspace":
			if len(m.logDraft) > 0 {
				m.logDraft = m.logDraft[:len(m.logDraft)-1]
			}
		default:
			if len(msg.Runes) > 0 {
				m.logDraft += string(msg.Runes)
			}
		}
	case overlayHelp:
		switch msg.String() {
		case "esc", "q", "?":
			m.overlay = overlayNone
		case "j", "down":
			m.scrollHelp(1)
		case "k", "up":
			m.scrollHelp(-1)
		case "pgdown":
			m.scrollHelp(max(1, m.helpBodyBudget()-1))
		case "pgup":
			m.scrollHelp(-max(1, m.helpBodyBudget()-1))
		case "g", "home":
			m.scrollHelp(-len(helpLines))
		case "G", "end":
			m.scrollHelp(len(helpLines))
		}
	case overlayAppLog:
		switch msg.String() {
		case "esc", "q":
			m.overlay = overlayNone
		case "j", "down":
			m.scrollAppLog(1)
		case "k", "up":
			m.scrollAppLog(-1)
		case "pgdown":
			m.scrollAppLog(max(1, m.appLogBodyBudget()-1))
		case "pgup":
			m.scrollAppLog(-max(1, m.appLogBodyBudget()-1))
		case "g", "home":
			m.scrollAppLog(-len(m.appLogLines))
		case "G", "end":
			m.scrollAppLog(len(m.appLogLines))
		}
	case overlayAbout:
		if msg.String() == "esc" || msg.String() == "q" || msg.String() == "A" {
			m.overlay = overlayNone
		}
	case overlayDashboard:
		switch msg.String() {
		case "esc", "q", "d":
			m.overlay = overlayNone
		case "j", "down":
			m.dashboardMoveCursor(1)
		case "k", "up":
			m.dashboardMoveCursor(-1)
		case "enter":
			return m.dashboardOpenSelected()
		case "p":
			return m.dashboardOpenProblems()
		}
	case overlayCommandPalette:
		return m.handleCommandPaletteKey(msg)
	case overlayThemePicker:
		switch m.themes.Update(msg) {
		case tideui.ThemePickerConfirm:
			m.theme = m.themes.ConfirmedTheme()
			m.overlay = overlayNone
			m.saveSettings()
			if !m.statusErr {
				m.status, m.statusErr = "theme: "+m.theme.Name, false
			}
		case tideui.ThemePickerCancel:
			m.theme = m.themes.ConfirmedTheme()
			m.overlay = overlayNone
		default:
			m.theme = m.themes.PreviewTheme()
		}
	case overlaySettings:
		return m.handleSettingsKey(msg)
	case overlaySystems:
		return m.handleSystemsKey(msg)
	case overlayCopy:
		return m.handleCopyKey(msg)
	case overlayOpen:
		return m.handleOpenKey(msg)
	case overlayCreate:
		return m.handleCreateKey(msg)
	case overlayDelete:
		return m.handleDeleteKey(msg)
	case overlayReplicate:
		return m.handleReplicateKey(msg)
	case overlayUpdate:
		return m.handleUpdateKey(msg)
	}
	return m, nil
}

// handleDeleteKey gates the Delete confirmation the same way Systems' own
// destructive delete does (systemModeDelete): esc/n/q cancels, y proceeds.
func (m Model) handleDeleteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "n", "q":
		m.overlay = overlayNone
	case "y":
		m.overlay = overlayNone
		return m.startDelete()
	}
	return m, nil
}

func (m Model) handleReplicateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "n", "q":
		m.overlay = overlayNone
	case "y":
		m.overlay = overlayNone
		return m.startReplicate()
	}
	return m, nil
}

// startDelete dispatches to the Compose (real removal: stop/remove the
// container, delete the service's definition — override and/or base file
// block) or standalone (real container removal) path depending on the
// selected container, mirroring the same Compose-vs-standalone test
// defaultCreateDraft already uses.
func (m Model) startDelete() (tea.Model, tea.Cmd) {
	selected := m.selectedContainer()
	if selected == nil {
		return m, nil
	}
	if selected.Compose.Project != "" {
		spec, err := composeSpecForSelected(selected, m.activeSystemConfig())
		if err != nil {
			m.status, m.statusErr = "delete "+selected.Compose.Service+": "+err.Error(), true
			return m, nil
		}
		m.busy = true
		m.status, m.statusErr = "deleting "+selected.Compose.Service+"…", false
		return m, m.deleteComposeCmd(spec)
	}
	provider := m.provider
	id := selected.ID
	label := "delete " + selected.DisplayName()
	m.busy = true
	m.status, m.statusErr = "deleting "+selected.DisplayName()+"…", false
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return actionDoneMsg{label: label, err: provider.RemoveContainer(ctx, id, true)}
	}
}

// startReplicate dispatches to the Compose (pull + up -d) or standalone
// (pull + remove + recreate with an identical spec) path. Standalone is the
// only path with direct Docker API access to real pull progress, so it
// wires PullImage's onProgress callback into m.replicateProgress (drained
// each statusPulseTickMsg tick, see drainReplicateProgress) instead of just
// a static phase label.
func (m Model) startReplicate() (tea.Model, tea.Cmd) {
	selected := m.selectedContainer()
	if selected == nil {
		return m, nil
	}
	if selected.Compose.Project != "" {
		spec, err := composeSpecForSelected(selected, m.activeSystemConfig())
		if err != nil {
			m.status, m.statusErr = "replicate "+selected.Compose.Service+": "+err.Error(), true
			return m, nil
		}
		m.busy = true
		m.status, m.statusErr = "replicating "+selected.Compose.Service+"…", false
		return m, m.replicateComposeCmd(spec)
	}
	spec, err := replicateContainerSpec(*selected)
	if err != nil {
		m.status, m.statusErr = "replicate "+selected.DisplayName()+": "+err.Error(), true
		return m, nil
	}
	provider := m.provider
	id := selected.ID
	image := selected.Image
	label := "replicate " + selected.DisplayName()
	progress := make(chan string, 16)
	m.busy = true
	m.replicateProgress = progress
	m.status, m.statusErr = "pulling "+image+"…", false
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute) // pulling an image can be slow
		defer cancel()
		onProgress := func(p app.PullProgress) {
			select {
			case progress <- formatPullProgress(image, p):
			default: // UI hasn't drained yet; drop, next tick catches up
			}
		}
		if err := provider.PullImage(ctx, image, onProgress); err != nil {
			return actionDoneMsg{label: label, err: err}
		}
		if err := provider.RemoveContainer(ctx, id, true); err != nil {
			return actionDoneMsg{label: label, err: err}
		}
		_, err := provider.CreateContainer(ctx, spec)
		return actionDoneMsg{label: label, err: err}
	}
}

func (m Model) handleCopyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "c":
		m.overlay = overlayNone
	case "up", "k":
		m.moveCopyCursor(-1)
	case "down", "j", "tab":
		m.moveCopyCursor(1)
	case "enter":
		rows := m.copyRows()
		if len(rows) == 0 {
			m.overlay = overlayNone
			m.status, m.statusErr = "nothing to copy", true
			return m, nil
		}
		row := rows[clamp(m.copyCursor, 0, len(rows)-1)]
		m.overlay = overlayNone
		m.status, m.statusErr = "copied "+strings.ToLower(row.label)+" "+short(row.value, 48), false
		return m, copyTextCmd(row.value)
	}
	return m, nil
}

func (m Model) handleOpenKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "o":
		m.overlay = overlayNone
	case "up", "k":
		m.moveOpenCursor(-1)
	case "down", "j", "tab":
		m.moveOpenCursor(1)
	case "enter":
		rows := m.openRows()
		if len(rows) == 0 {
			m.overlay = overlayNone
			m.status, m.statusErr = "nothing to open", true
			return m, nil
		}
		row := rows[clamp(m.openCursor, 0, len(rows)-1)]
		m.overlay = overlayNone
		m.status, m.statusErr = "opening "+strings.ToLower(row.label)+" "+short(row.value, 48), false
		return m, openTargetCmd(row.label, row.target)
	}
	return m, nil
}

func (m Model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.settingsEditingField != "" {
		return m.handleSettingsTextEditKey(msg)
	}
	switch msg.String() {
	case "esc", "q", ",", "ctrl+,":
		m.settingsDraft = m.settings
		m.overlay = overlayNone
	case "up", "k":
		m.moveSettingsCursor(-1)
	case "down", "j", "tab":
		m.moveSettingsCursor(1)
	case "left", "h":
		m.cycleSetting(m.settingsCursor, -1)
	case "right", "l", "enter", " ":
		rows := m.settingsRows()
		idx := clamp(m.settingsCursor, 0, len(rows)-1)
		row := rows[idx]
		switch row.action {
		case settingsActionCheckUpdate:
			m.updateChecking = true
			m.updateCheckErr = nil
			return m, m.checkForUpdateCmd(true)
		case settingsActionViewAppLog:
			m.overlay = overlayAppLog
			m.appLogScroll = max(0, len(m.appLogLines)-m.appLogBodyBudget())
			return m, nil
		}
		if row.kind == settingsRowText || row.kind == settingsRowSecretText {
			m.startSettingsTextEdit(row.label)
			return m, nil
		}
		m.cycleSetting(m.settingsCursor, 1)
	case "ctrl+s":
		m.saveSettingsDraft()
	}
	return m, nil
}

// startSettingsTextEdit opens inline editing for a settingsRowText/
// settingsRowSecretText row — draft seeded from its current stored value
// (including the actual secret for "AI API key", so backspacing edits it
// rather than always starting from scratch) with the cursor at the end.
func (m *Model) startSettingsTextEdit(label string) {
	m.settingsEditingField = label
	m.settingsEditDraft = m.settingsTextFieldValue(label)
	m.settingsEditCursor = len([]rune(m.settingsEditDraft))
}

func (m Model) settingsTextFieldValue(label string) string {
	switch label {
	case "AI model":
		return m.settingsDraft.AIModel
	case "AI API key":
		return m.settingsDraft.AIAPIKey
	case "AI base URL":
		return m.settingsDraft.AIBaseURL
	default:
		return ""
	}
}

func (m *Model) setSettingsTextFieldValue(label, value string) {
	switch label {
	case "AI model":
		m.settingsDraft.AIModel = value
	case "AI API key":
		m.settingsDraft.AIAPIKey = value
	case "AI base URL":
		m.settingsDraft.AIBaseURL = value
	}
}

// handleSettingsTextEditKey handles keys while settingsEditingField is
// set — a small text editor (typing, backspace/delete, left/right/home/end,
// ctrl+u to clear) mirroring the Create form's field-editing keys, scoped
// down to what a one-line settings value actually needs. Enter commits the
// draft back into settingsDraft; Esc discards it.
func (m Model) handleSettingsTextEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.settingsEditingField = ""
		m.settingsEditDraft = ""
		m.settingsEditCursor = 0
	case "enter":
		m.setSettingsTextFieldValue(m.settingsEditingField, m.settingsEditDraft)
		m.settingsEditingField = ""
		m.settingsEditDraft = ""
		m.settingsEditCursor = 0
	case "left":
		m.settingsEditCursor = max(0, m.settingsEditCursor-1)
	case "right":
		m.settingsEditCursor = min(len([]rune(m.settingsEditDraft)), m.settingsEditCursor+1)
	case "home", "ctrl+a":
		m.settingsEditCursor = 0
	case "end", "ctrl+e":
		m.settingsEditCursor = len([]rune(m.settingsEditDraft))
	case "backspace":
		runes := []rune(m.settingsEditDraft)
		if m.settingsEditCursor > 0 && m.settingsEditCursor <= len(runes) {
			runes = append(runes[:m.settingsEditCursor-1], runes[m.settingsEditCursor:]...)
			m.settingsEditDraft = string(runes)
			m.settingsEditCursor--
		}
	case "delete":
		runes := []rune(m.settingsEditDraft)
		if m.settingsEditCursor < len(runes) {
			runes = append(runes[:m.settingsEditCursor], runes[m.settingsEditCursor+1:]...)
			m.settingsEditDraft = string(runes)
		}
	case "ctrl+u":
		m.settingsEditDraft = ""
		m.settingsEditCursor = 0
	default:
		if len(msg.Runes) > 0 {
			runes := []rune(m.settingsEditDraft)
			cursor := clamp(m.settingsEditCursor, 0, len(runes))
			merged := append([]rune{}, runes[:cursor]...)
			merged = append(merged, msg.Runes...)
			merged = append(merged, runes[cursor:]...)
			m.settingsEditDraft = string(merged)
			m.settingsEditCursor = cursor + len(msg.Runes)
		}
	}
	return m, nil
}

// settingsEditValueWithCaret renders the in-progress edit for row with a
// caret at the cursor position — masked as bullets for settingsRowSecretText
// (a real password-style field, not just masked when idle), plain text
// otherwise. Mirrors systemFieldValueWithCaret's caret convention.
func (m Model) settingsEditValueWithCaret(row settingsRow) string {
	runes := []rune(m.settingsEditDraft)
	if row.kind == settingsRowSecretText {
		runes = []rune(strings.Repeat("•", len(runes)))
	}
	cursor := clamp(m.settingsEditCursor, 0, len(runes))
	withCaret := append([]rune{}, runes[:cursor]...)
	withCaret = append(withCaret, '|')
	withCaret = append(withCaret, runes[cursor:]...)
	return string(withCaret)
}

func (m Model) handleSystemsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.systemMode {
	case systemModeList:
		switch msg.String() {
		case "esc", "q", "S":
			m.overlay = overlayNone
		case "up", "k":
			m.moveSystemsCursor(-1)
		case "down", "j", "tab":
			m.moveSystemsCursor(1)
		case "enter":
			system := m.currentSystem()
			if err := validateSystem(system); err != nil {
				m.status, m.statusErr = "system: "+err.Error(), true
				return m, nil
			}
			m.status, m.statusErr = systemSwitchStatus(system), false
			return m, m.switchSystemCmd(system)
		case "t":
			system := m.currentSystem()
			if err := validateSystem(system); err != nil {
				m.status, m.statusErr = "test "+system.Name+": "+err.Error(), true
				return m, nil
			}
			m.status, m.statusErr = systemTestStatus(system), false
			return m, m.testSystemCmd(system)
		case "a":
			m.startAddSystem()
		case "e":
			m.startEditSystem()
		case "d":
			if len(m.systems) > 1 && m.currentSystem().ID != m.activeSystem {
				m.systemMode = systemModeDelete
			}
		}
	case systemModeEdit:
		switch msg.String() {
		case "esc":
			m.systemMode = systemModeList
		case "up":
			m.moveSystemField(-1)
		case "down", "tab":
			m.moveSystemField(1)
		case "k":
			if m.isSystemChoiceField() {
				m.moveSystemField(-1)
				return m, nil
			}
			m.editSystemFieldString("k")
		case "j":
			if m.isSystemChoiceField() {
				m.moveSystemField(1)
				return m, nil
			}
			m.editSystemFieldString("j")
		case "left":
			if m.isSystemChoiceField() {
				m.cycleSystemChoice()
			} else {
				m.moveSystemCursor(-1)
			}
		case "right":
			if m.isSystemChoiceField() {
				m.cycleSystemChoice()
			} else {
				m.moveSystemCursor(1)
			}
		case "h":
			if m.isSystemChoiceField() {
				m.cycleSystemChoice()
				return m, nil
			}
			m.editSystemFieldString("h")
		case "l":
			if m.isSystemChoiceField() {
				m.cycleSystemChoice()
				return m, nil
			}
			m.editSystemFieldString("l")
		case "enter":
			if m.isSystemChoiceField() {
				m.cycleSystemChoice()
				return m, nil
			}
			m.moveSystemField(1)
		case "ctrl+s":
			m.saveSystemDraft()
		case "backspace":
			m.editSystemFieldBackspace()
		case "delete":
			m.editSystemFieldDelete()
		case "home", "ctrl+a":
			m.systemCursor = 0
		case "end", "ctrl+e":
			m.systemCursor = len([]rune(m.systemFieldValue()))
		case "ctrl+u":
			if !m.isSystemChoiceField() {
				m.setSystemFieldValue("")
				m.systemCursor = 0
			}
		default:
			if len(msg.Runes) > 0 {
				m.editSystemFieldString(string(msg.Runes))
			}
		}
	case systemModeDelete:
		switch msg.String() {
		case "esc", "n", "q":
			m.systemMode = systemModeList
		case "y":
			m.deleteCurrentSystem()
		}
	}
	return m, nil
}

func (m *Model) openSystemsOverlay() {
	m.systems = config.NormalizeSystems(config.Settings{ActiveSystem: m.activeSystem, Systems: m.systems}).Systems
	m.activeSystem = config.NormalizeSystems(config.Settings{ActiveSystem: m.activeSystem, Systems: m.systems}).ActiveSystem
	m.overlay = overlaySystems
	m.systemMode = systemModeList
	m.systemsCursor = clamp(m.systemsCursor, 0, len(m.systems)-1)
}

func (m *Model) moveSystemsCursor(delta int) {
	if len(m.systems) == 0 {
		m.systemsCursor = 0
		return
	}
	m.systemsCursor = modIndex(m.systemsCursor+delta, len(m.systems))
}

func (m Model) currentSystem() config.System {
	systems := config.NormalizeSystems(config.Settings{ActiveSystem: m.activeSystem, Systems: m.systems}).Systems
	if len(systems) == 0 {
		return config.DefaultSystem()
	}
	return systems[clamp(m.systemsCursor, 0, len(systems)-1)]
}

func (m *Model) startAddSystem() {
	next := len(m.systems) + 1
	m.systemDraft = config.System{
		ID:           fmt.Sprintf("remote-%d", next),
		Name:         fmt.Sprintf("remote %d", next),
		Kind:         "ssh",
		RemoteSocket: "/var/run/docker.sock",
		LocalSocket:  filepath.Join(os.TempDir(), fmt.Sprintf("whatthedock-remote-%d.sock", next)),
	}
	m.systemDraftNew = true
	m.systemField = systemFieldName
	m.systemCursor = len([]rune(m.systemDraft.Name))
	m.systemMode = systemModeEdit
}

func (m *Model) startEditSystem() {
	m.systemDraft = m.currentSystem()
	m.systemDraftNew = false
	m.systemField = systemFieldName
	m.systemCursor = len([]rune(m.systemDraft.Name))
	m.systemMode = systemModeEdit
}

func (m *Model) moveSystemField(delta int) {
	fields := m.visibleSystemFields()
	if len(fields) == 0 {
		m.systemField = systemFieldName
		m.systemCursor = 0
		return
	}
	current := 0
	for i, field := range fields {
		if field == m.systemField {
			current = i
			break
		}
	}
	m.systemField = fields[modIndex(current+delta, len(fields))]
	m.systemCursor = len([]rune(m.systemFieldValue()))
}

func (m Model) visibleSystemFields() []systemField {
	fields := []systemField{systemFieldName, systemFieldKind}
	if m.systemDraft.Kind == "ssh" {
		return append(fields, systemFieldSSHHost, systemFieldSSHUser, systemFieldSSHPort, systemFieldSSHAuth, systemFieldRemoteSocket, systemFieldLocalSocket)
	}
	return append(fields, systemFieldDockerHost)
}

func (m Model) isSystemChoiceField() bool {
	return m.systemField == systemFieldKind || m.systemField == systemFieldSSHAuth
}

func (m *Model) cycleSystemChoice() {
	switch m.systemField {
	case systemFieldKind:
		m.toggleSystemKind()
	case systemFieldSSHAuth:
		m.toggleSystemAuth()
	}
}

func (m *Model) toggleSystemKind() {
	if m.systemDraft.Kind == "ssh" {
		m.systemDraft.Kind = "local"
		m.systemDraft.SSHAuth = ""
		return
	}
	m.systemDraft.Kind = "ssh"
	if m.systemDraft.SSHAuth == "" {
		m.systemDraft.SSHAuth = "config"
	}
	if m.systemDraft.RemoteSocket == "" {
		m.systemDraft.RemoteSocket = "/var/run/docker.sock"
	}
	if m.systemDraft.LocalSocket == "" {
		id := m.systemDraft.ID
		if id == "" {
			id = "remote"
		}
		m.systemDraft.LocalSocket = filepath.Join(os.TempDir(), "whatthedock-"+id+".sock")
	}
}

func (m *Model) toggleSystemAuth() {
	if m.systemDraft.Kind != "ssh" {
		return
	}
	if m.systemDraft.SSHAuth == "password" {
		m.systemDraft.SSHAuth = "config"
		return
	}
	m.systemDraft.SSHAuth = "password"
}

func (m *Model) editSystemFieldBackspace() {
	value := m.systemFieldValue()
	runes := []rune(value)
	m.systemCursor = clamp(m.systemCursor, 0, len(runes))
	if m.systemCursor == 0 {
		return
	}
	runes = append(runes[:m.systemCursor-1], runes[m.systemCursor:]...)
	m.systemCursor--
	m.setSystemFieldValue(string(runes))
}

func (m *Model) editSystemFieldDelete() {
	if m.isSystemChoiceField() {
		return
	}
	runes := []rune(m.systemFieldValue())
	m.systemCursor = clamp(m.systemCursor, 0, len(runes))
	if m.systemCursor >= len(runes) {
		return
	}
	runes = append(runes[:m.systemCursor], runes[m.systemCursor+1:]...)
	m.setSystemFieldValue(string(runes))
}

func (m *Model) editSystemFieldString(value string) {
	if m.isSystemChoiceField() {
		return
	}
	runes := []rune(m.systemFieldValue())
	insert := []rune(value)
	m.systemCursor = clamp(m.systemCursor, 0, len(runes))
	updated := append([]rune{}, runes[:m.systemCursor]...)
	updated = append(updated, insert...)
	updated = append(updated, runes[m.systemCursor:]...)
	m.systemCursor += len(insert)
	m.setSystemFieldValue(string(updated))
}

func (m *Model) moveSystemCursor(delta int) {
	if m.isSystemChoiceField() {
		return
	}
	m.systemCursor = clamp(m.systemCursor+delta, 0, len([]rune(m.systemFieldValue())))
}

func (m Model) systemFieldValue() string {
	switch m.systemField {
	case systemFieldName:
		return m.systemDraft.Name
	case systemFieldDockerHost:
		return m.systemDraft.DockerHost
	case systemFieldSSHHost:
		return m.systemDraft.SSHHost
	case systemFieldSSHUser:
		return m.systemDraft.SSHUser
	case systemFieldSSHPort:
		return m.systemDraft.SSHPort
	case systemFieldSSHAuth:
		return systemAuthLabel(m.systemDraft.SSHAuth)
	case systemFieldRemoteSocket:
		return m.systemDraft.RemoteSocket
	case systemFieldLocalSocket:
		return m.systemDraft.LocalSocket
	default:
		return m.systemDraft.Kind
	}
}

func (m Model) systemFieldValueWithCaret() string {
	runes := []rune(m.systemFieldValue())
	cursor := clamp(m.systemCursor, 0, len(runes))
	withCaret := append([]rune{}, runes[:cursor]...)
	withCaret = append(withCaret, '|')
	withCaret = append(withCaret, runes[cursor:]...)
	return string(withCaret)
}

func (m *Model) setSystemFieldValue(value string) {
	switch m.systemField {
	case systemFieldName:
		m.systemDraft.Name = value
	case systemFieldDockerHost:
		m.systemDraft.DockerHost = value
	case systemFieldSSHHost:
		m.systemDraft.SSHHost = value
	case systemFieldSSHUser:
		m.systemDraft.SSHUser = value
	case systemFieldSSHPort:
		m.systemDraft.SSHPort = value
	case systemFieldRemoteSocket:
		m.systemDraft.RemoteSocket = value
	case systemFieldLocalSocket:
		m.systemDraft.LocalSocket = value
	}
}

func (m *Model) saveSystemDraft() {
	m.systemDraft = config.NormalizeSystems(config.Settings{ActiveSystem: m.systemDraft.ID, Systems: []config.System{m.systemDraft}}).Systems[0]
	if err := validateSystem(m.systemDraft); err != nil {
		m.status, m.statusErr = "system: "+err.Error(), true
		return
	}
	if m.systemDraftNew {
		m.systems = append(m.systems, m.systemDraft)
		m.systemsCursor = len(m.systems) - 1
	} else {
		index := clamp(m.systemsCursor, 0, len(m.systems)-1)
		m.systems[index] = m.systemDraft
	}
	m.systemMode = systemModeList
	m.saveSettings()
}

func (m *Model) deleteCurrentSystem() {
	if len(m.systems) <= 1 {
		m.systemMode = systemModeList
		return
	}
	index := clamp(m.systemsCursor, 0, len(m.systems)-1)
	deleted := m.systems[index]
	if deleted.ID == m.activeSystem {
		m.systemMode = systemModeList
		m.status, m.statusErr = "switch systems before deleting the active system", true
		return
	}
	m.systems = append(m.systems[:index], m.systems[index+1:]...)
	m.systemsCursor = clamp(index, 0, len(m.systems)-1)
	m.systemMode = systemModeList
	m.saveSettings()
}

func (m Model) switchSystemCmd(system config.System) tea.Cmd {
	if system.ID == "" || system.ID == m.activeSystem {
		return nil
	}
	if m.providerFor == nil {
		return func() tea.Msg {
			return systemSwitchMsg{system: system, err: errors.New("system switching is unavailable in this build")}
		}
	}
	if system.Kind == "ssh" && system.SSHAuth == "password" {
		cmd, err := systems.SSHCommand(system)
		if err != nil {
			return func() tea.Msg {
				return systemSwitchMsg{system: system, err: err}
			}
		}
		if cmd != nil {
			return tea.ExecProcess(cmd, func(err error) tea.Msg {
				return systemTunnelMsg{system: system, err: err}
			})
		}
	}
	return m.providerSwitchCmd(system)
}

func (m Model) testSystemCmd(system config.System) tea.Cmd {
	if m.providerFor == nil {
		return func() tea.Msg {
			return systemTestMsg{system: system, err: errors.New("system testing is unavailable in this build")}
		}
	}
	if system.Kind == "ssh" && system.SSHAuth == "password" {
		cmd, err := systems.SSHCommand(system)
		if err != nil {
			return func() tea.Msg {
				return systemTestMsg{system: system, err: err}
			}
		}
		if cmd != nil {
			return tea.ExecProcess(cmd, func(err error) tea.Msg {
				return systemTunnelMsg{system: system, test: true, err: err}
			})
		}
	}
	return m.providerTestCmd(system)
}

// execShellCommand builds the docker exec subprocess for dropping into a
// running container's shell. Prefers bash if present, falling back to sh,
// since most images have one or the other but rarely lack both. An empty
// DOCKER_HOST from systems.DockerHostFor means "use Docker's own default
// resolution" — leave the subprocess env untouched rather than overriding
// it with an empty value.
func execShellCommand(system config.System, id domain.ResourceID) *exec.Cmd {
	cmd := exec.Command("docker", "exec", "-it", id.ID, "sh", "-c", "[ -x /bin/bash ] && exec bash || exec sh")
	if host := systems.DockerHostFor(system); host != "" {
		cmd.Env = append(os.Environ(), "DOCKER_HOST="+host)
	}
	return cmd
}

// execShellCmd hands the real terminal over to docker exec for an
// interactive session — the same tea.ExecProcess mechanism already used for
// the SSH password prompt (see switchSystemCmd), just for a foreground
// session the user directly controls instead of a backgrounded tunnel.
func (m Model) execShellCmd(selected domain.Container) tea.Cmd {
	cmd := execShellCommand(m.activeSystemConfig(), selected.ID)
	name := selected.DisplayName()
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return execShellDoneMsg{name: name, err: err}
	})
}

func (m Model) providerSwitchCmd(system config.System) tea.Cmd {
	factory := m.providerFor
	return func() tea.Msg {
		provider, err := factory(context.Background(), system)
		if err == nil && provider == nil {
			err = errors.New("system provider is unavailable")
		}
		return systemSwitchMsg{system: system, provider: provider, err: err}
	}
}

func (m Model) providerTestCmd(system config.System) tea.Cmd {
	factory := m.providerFor
	return func() tea.Msg {
		ctx := context.Background()
		provider, err := factory(ctx, system)
		if err != nil {
			return systemTestMsg{system: system, err: err}
		}
		if provider == nil {
			return systemTestMsg{system: system, err: errors.New("system provider is unavailable")}
		}
		defer provider.Close()
		return systemTestMsg{system: system, err: provider.Ping(ctx)}
	}
}

func validateSystem(system config.System) error {
	system = config.NormalizeSystems(config.Settings{ActiveSystem: system.ID, Systems: []config.System{system}}).Systems[0]
	if strings.TrimSpace(system.Name) == "" {
		return errors.New("name is required")
	}
	switch system.Kind {
	case "ssh":
		if strings.TrimSpace(system.SSHHost) == "" {
			return errors.New("ssh host is required")
		}
		if strings.TrimSpace(system.SSHPort) != "" {
			port, err := strconv.Atoi(system.SSHPort)
			if err != nil || port < 1 || port > 65535 {
				return errors.New("ssh port must be 1-65535")
			}
		}
		if strings.TrimSpace(system.RemoteSocket) == "" {
			return errors.New("remote socket is required")
		}
		if strings.TrimSpace(system.LocalSocket) == "" {
			return errors.New("local socket is required")
		}
	case "local", "":
	default:
		return fmt.Errorf("unknown system kind %q", system.Kind)
	}
	return nil
}

func systemSwitchStatus(system config.System) string {
	if system.Kind == "ssh" && system.SSHAuth == "password" {
		return "opening SSH password prompt for " + system.Name
	}
	return "switching system to " + system.Name
}

func systemTestStatus(system config.System) string {
	if system.Kind == "ssh" && system.SSHAuth == "password" {
		return "opening SSH password prompt to test " + system.Name
	}
	return "testing system " + system.Name
}

func systemAuthLabel(auth string) string {
	if auth == "password" {
		return "password prompt"
	}
	return "config/agent"
}

func (m *Model) resetDockerState() {
	m.loading = true
	m.snapshot = domain.Snapshot{}
	m.rows = nil
	m.cursor = 0
	m.focusedTreeKey = treeRowKey{}
	m.problemCursor = 0
	m.selectedID = domain.ResourceID{}
	m.selected = nil
	m.inspectorScroll = 0
	m.logLines = nil
	m.logChan = nil
	m.logCancel = nil
	m.logErr = nil
	m.logLoading = false
	m.logReplaceOnDrain = false
	m.stats = nil
	m.statsID = domain.ResourceID{}
	m.statsHistory = map[domain.ResourceID]statsHistory{}
	m.statsLoading = false
	m.statsErr = nil
	m.eventChan = nil
	m.eventCancel = nil
	m.snapshotDirty = false
	m.eventBackoff = 0
}

func (m Model) handleCommandPaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.filteredCommands()
	switch msg.String() {
	case "esc":
		m.overlay = overlayNone
	case "up", "k":
		if len(items) > 0 {
			m.commandCursor = (m.commandCursor - 1 + len(items)) % len(items)
		}
	case "down", "j", "tab":
		if len(items) > 0 {
			m.commandCursor = (m.commandCursor + 1) % len(items)
		}
	case "enter":
		if len(items) == 0 {
			return m, nil
		}
		item := items[clamp(m.commandCursor, 0, len(items)-1)]
		if !item.Enabled {
			return m, nil
		}
		m.overlay = overlayNone
		return m.executeCommand(item.ID)
	case "backspace":
		if len(m.commandFilter) > 0 {
			m.commandFilter = m.commandFilter[:len(m.commandFilter)-1]
			m.commandCursor = 0
		}
	default:
		if len(msg.Runes) > 0 {
			m.commandFilter += string(msg.Runes)
			m.commandCursor = 0
		}
	}
	return m, nil
}

func (m Model) executeCommand(id actions.ID) (tea.Model, tea.Cmd) {
	switch id {
	case actions.Refresh:
		return m, m.refreshCmd()
	case actions.Create:
		return m, m.openCreateOverlay()
	case actions.StartStop:
		return m, m.actionCmd(actions.StartStop, "start/stop")
	case actions.Restart:
		return m, m.actionCmd(actions.Restart, "restart")
	case actions.Delete:
		if selected := m.selectedContainer(); selected != nil {
			m.overlay = overlayDelete
		}
	case actions.Replicate:
		if selected := m.selectedContainer(); selected != nil {
			m.overlay = overlayReplicate
		}
	case actions.Clone:
		if selected := m.selectedContainer(); selected != nil {
			m.openCloneOverlay()
		}
	case actions.ExecShell:
		if selected := m.selectedContainer(); selected != nil && selected.IsRunning() {
			return m, m.execShellCmd(*selected)
		}
	case actions.FocusLogs:
		m.focus = paneActivity
		m.mode = activityLogs
		if m.selected != nil {
			return m, m.startLogsCmd(m.selected.ID)
		}
	case actions.ShowProblems:
		m.focus = paneActivity
		m.mode = activityProblems
		m.syncProblemCursor()
	case actions.ShowStats:
		m.focus = paneActivity
		m.mode = activityStats
		if selected := m.selectedContainer(); selected != nil {
			m.selectedID = selected.ID
			m.statsLoading = true
			m.statsErr = nil
			return m, m.loadStatsCmd(selected.ID)
		}
	case actions.OpenCopy:
		m.openCopyOverlay()
	case actions.OpenPort:
		m.openOpenOverlayFor(openKindPort)
	case actions.OpenMount:
		m.openOpenOverlayFor(openKindMount)
	case actions.OpenFilter:
		m.overlay = overlayFilter
		m.filterDraft = m.filter
	case actions.OpenLogFilter:
		m.focus = paneActivity
		m.mode = activityLogs
		m.openLogFilter()
		if m.selected != nil && m.logChan == nil && len(m.logLines) == 0 {
			return m, m.startLogsCmd(m.selected.ID)
		}
	case actions.OpenHelp:
		m.overlay = overlayHelp
		m.helpScroll = 0
	case actions.OpenAbout:
		return m.openAboutOverlay()
	case actions.OpenTheme:
		m.openThemePicker()
	case actions.OpenSettings:
		m.openSettingsOverlay()
	case actions.OpenSystems:
		m.openSystemsOverlay()
	case actions.CommandPalette:
		m.overlay = overlayCommandPalette
		m.commandFilter = ""
		m.commandCursor = 0
	case actions.Quit:
		m.cleanup()
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) openAboutOverlay() (tea.Model, tea.Cmd) {
	m.overlay = overlayAbout
	m.aboutFrame = 0
	m.aboutSpotlights = newAboutSpotlights(aboutSpotlightCount, len(aboutLogo()), aboutContentWidth(m.width))
	return m, tickAbout()
}

// openDashboardOverlay opens the full-screen fleet dashboard and kicks off
// its own stats-polling loop immediately (see dashboardRefreshCmd) rather
// than waiting a full StatsRefresh interval for the first paint — same
// load-on-entry shape as opening the single-container Stats pane.
func (m Model) openDashboardOverlay() (tea.Model, tea.Cmd) {
	m.overlay = overlayDashboard
	m.dashboardCursor = 0
	return m, m.dashboardRefreshCmd()
}

func (m *Model) openThemePicker() {
	m.themes.Open(m.theme.Name)
	m.overlay = overlayThemePicker
}

func (m *Model) openSettingsOverlay() {
	m.settingsDraft = m.settings
	m.overlay = overlaySettings
	m.settingsCursor = m.firstSettingsRow()
}

func (m *Model) openCopyOverlay() {
	if m.selectedContainer() == nil {
		m.status, m.statusErr = "no container selected", true
		return
	}
	m.overlay = overlayCopy
	m.copyCursor = clamp(m.copyCursor, 0, len(m.copyRows())-1)
}

func (m *Model) openOpenOverlay() {
	if m.selectedContainer() == nil {
		m.status, m.statusErr = "no container selected", true
		return
	}
	rows := m.openRows()
	if len(rows) == 0 {
		m.status, m.statusErr = "nothing openable for selected container", true
		return
	}
	m.overlay = overlayOpen
	m.openCursor = clamp(m.openCursor, 0, len(rows)-1)
}

func (m *Model) openOpenOverlayFor(kind openKind) {
	m.openOpenOverlay()
	if m.overlay != overlayOpen {
		return
	}
	for i, row := range m.openRows() {
		if row.kind == kind {
			m.openCursor = i
			return
		}
	}
}

func (m Model) copyRows() []copyRow {
	ctr := m.selectedContainer()
	if ctr == nil {
		return nil
	}
	var rows []copyRow
	add := func(label, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			rows = append(rows, copyRow{label: label, value: value})
		}
	}

	// AI analysis only shows up here once it actually belongs to ctr — the
	// same aiAnalysisFor guard problemInsightText uses, so opening Copy
	// right after Enter-selecting the problem row you just ran "a" on (the
	// normal way to act on a highlighted problem with any general
	// container action, same as l/g/D/etc.) surfaces it, but Copy on some
	// other container never shows a mismatched result under the wrong one.
	if m.aiAnalysisFor == ctr.ID {
		add("AI analysis", m.aiAnalysis)
	}

	add("Container ID", ctr.ID.ID)
	add("Name", ctr.DisplayName())
	add("Image", ctr.Image)
	add("Image ID", ctr.ImageID)
	add("Compose project", ctr.Compose.Project)
	add("Compose service", ctr.Compose.Service)
	add("Compose number", ctr.Compose.ContainerNumber)
	add("Compose config", ctr.Compose.ConfigFiles)
	for _, port := range ctr.Ports {
		add("Port", copyPortValue(port))
	}
	for _, mount := range ctr.Mounts {
		add("Mount", copyMountValue(mount))
	}
	keys := make([]string, 0, len(ctr.Labels))
	for key := range ctr.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		add("Label "+key, key+"="+ctr.Labels[key])
	}
	return rows
}

func (m *Model) moveCopyCursor(delta int) {
	rows := m.copyRows()
	if len(rows) == 0 {
		m.copyCursor = 0
		return
	}
	m.copyCursor = (m.copyCursor + delta + len(rows)) % len(rows)
}

func copyPortValue(port domain.Port) string {
	private := fmt.Sprintf("%d/%s", port.Private, port.Type)
	if port.Public <= 0 {
		return private
	}
	public := fmt.Sprintf("%d", port.Public)
	if strings.TrimSpace(port.IP) != "" {
		public = port.IP + ":" + public
	}
	return public + " -> " + private
}

func copyMountValue(mount domain.Mount) string {
	value := mount.Source + " -> " + mount.Destination
	var meta []string
	if mount.Type != "" {
		meta = append(meta, mount.Type)
	}
	if mount.Mode != "" {
		meta = append(meta, mount.Mode)
	}
	if !mount.ReadWrite {
		meta = append(meta, "ro")
	}
	if len(meta) > 0 {
		value += " (" + strings.Join(meta, ", ") + ")"
	}
	return value
}

func copyTextCmd(value string) tea.Cmd {
	sequence := osc52.New(value).String()
	return func() tea.Msg {
		_, _ = io.WriteString(clipboardWriter, sequence)
		return nil
	}
}

func (m Model) openRows() []openRow {
	ctr := m.selectedContainer()
	if ctr == nil {
		return nil
	}
	var rows []openRow
	for _, port := range ctr.Ports {
		target := portOpenTarget(port)
		if target == "" {
			continue
		}
		rows = append(rows, openRow{kind: openKindPort, label: "Port", value: copyPortValue(port), target: target})
	}
	for _, mount := range ctr.Mounts {
		target := strings.TrimSpace(mount.Source)
		if target == "" {
			target = strings.TrimSpace(mount.Destination)
		}
		if target != "" {
			rows = append(rows, openRow{kind: openKindMount, label: "Mount", value: copyMountValue(mount), target: target})
		}
	}
	for _, path := range splitComposeConfigFiles(ctr.Compose.ConfigFiles) {
		rows = append(rows, openRow{kind: openKindMount, label: "Compose config", value: path, target: path})
	}
	return rows
}

func (m *Model) moveOpenCursor(delta int) {
	rows := m.openRows()
	if len(rows) == 0 {
		m.openCursor = 0
		return
	}
	m.openCursor = (m.openCursor + delta + len(rows)) % len(rows)
}

func portOpenTarget(port domain.Port) string {
	if port.Public <= 0 {
		return ""
	}
	host := strings.TrimSpace(port.IP)
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "localhost"
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%d", host, port.Public)
}

func splitComposeConfigFiles(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func openTargetCmd(label, target string) tea.Cmd {
	return func() tea.Msg {
		if err := openTarget(target); err != nil {
			return openDoneMsg{label: strings.ToLower(label), err: err}
		}
		return openDoneMsg{label: strings.ToLower(label)}
	}
}

func defaultOpenTarget(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

func (m Model) settingsRows() []settingsRow {
	settings := m.settingsForRows()
	rows := []settingsRow{
		{label: "Stats", kind: settingsRowSection},
		{label: "Graph style", value: settings.GraphStyle.String()},
		{label: "Graph color", value: settings.GraphColor.String()},
		{label: "Show deltas", value: onOff(settings.ShowDeltas)},
		{label: "Stats refresh", value: formatRefreshInterval(settings.StatsRefresh)},
		{label: "Logs", kind: settingsRowSection},
		{label: "Log color", value: settings.LogColor.String()},
		{label: "Log health color", value: onOff(settings.LogHealthColor)},
		{label: "Behavior", kind: settingsRowSection},
		{label: "Default pane", value: activityModeName(settings.DefaultActivity)},
		{label: "Start in dashboard", value: onOff(settings.StartInDashboard)},
		{label: "Modal shadow", value: onOff(settings.ModalShadow)},
		{label: "Editor", kind: settingsRowSection},
		{label: "Vim mode", value: onOff(settings.CreateVim)},
		{label: "AI", kind: settingsRowSection},
		{label: "AI provider", value: settings.AIProvider.String()},
		{label: "AI model", value: emptyAs(short(settings.AIModel, 30), "(provider default)"), kind: settingsRowText},
		{label: "AI API key", value: maskedSecret(settings.AIAPIKey), kind: settingsRowSecretText},
	}
	if settings.AIProvider == aiProviderCustom {
		rows = append(rows, settingsRow{label: "AI base URL", value: emptyAs(short(settings.AIBaseURL, 30), "(not set)"), kind: settingsRowText})
	}
	rows = append(rows,
		settingsRow{label: "Diagnostics", kind: settingsRowSection},
		settingsRow{label: "App log", value: settings.AppLog.String()},
		settingsRow{label: "View app log", value: "view", kind: settingsRowAction, action: settingsActionViewAppLog},
		settingsRow{label: "Maintenance", kind: settingsRowSection},
		settingsRow{label: "Reset defaults", value: "apply", kind: settingsRowAction, action: settingsActionResetDefaults},
		settingsRow{label: "Check for update", value: m.updateCheckRowValue(), kind: settingsRowAction, action: settingsActionCheckUpdate},
	)
	return rows
}

// maskedSecret is the "AI API key" row's display value outside of active
// editing — a fixed-length mask regardless of the real key's length, so
// the settings overlay never leaks even how long the stored key is.
func maskedSecret(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(not set)"
	}
	return strings.Repeat("•", 8)
}

// updateCheckRowValue is the "Check for update" settings row's right-hand
// value — reflects whatever the most recent check (automatic or manual)
// found, or "check" as the idle prompt before any check has run this
// session.
func (m Model) updateCheckRowValue() string {
	switch {
	case m.updateChecking:
		return "checking…"
	case m.updateCheckErr != nil:
		return "check failed"
	case m.updateAvailableVersion != "":
		return m.updateAvailableVersion + " available"
	case !m.updateLastCheck.IsZero():
		return "up to date"
	default:
		return "check"
	}
}

func (m Model) settingsForRows() appSettings {
	if m.overlay == overlaySettings {
		return m.settingsDraft
	}
	return m.settings
}

func (m *Model) moveSettingsCursor(delta int) {
	rows := m.settingsRows()
	if len(rows) == 0 {
		m.settingsCursor = 0
		return
	}
	for i := 0; i < len(rows); i++ {
		m.settingsCursor = modIndex(m.settingsCursor+delta, len(rows))
		if rows[m.settingsCursor].kind != settingsRowSection {
			return
		}
	}
	m.settingsCursor = 0
}

func (m Model) firstSettingsRow() int {
	for i, row := range m.settingsRows() {
		if row.kind != settingsRowSection {
			return i
		}
	}
	return 0
}

func (m *Model) cycleSetting(index, direction int) {
	if direction == 0 {
		direction = 1
	}
	rows := m.settingsRows()
	if len(rows) == 0 {
		return
	}
	row := rows[clamp(index, 0, len(rows)-1)]
	if row.kind == settingsRowSection {
		return
	}
	if row.action == settingsActionResetDefaults {
		m.settingsDraft = defaultSettings()
		m.settingsCursor = clamp(index, 0, len(m.settingsRows())-1)
		m.status, m.statusErr = "settings reset staged", false
		return
	}
	switch row.label {
	case "Graph style":
		m.settingsDraft.GraphStyle = graphStyle(modIndex(int(m.settingsDraft.GraphStyle)+direction, 5))
	case "Graph color":
		m.settingsDraft.GraphColor = graphColorMode(modIndex(int(m.settingsDraft.GraphColor)+direction, 3))
	case "Log color":
		m.settingsDraft.LogColor = logColorMode(modIndex(int(m.settingsDraft.LogColor)+direction, 4))
	case "Log health color":
		m.settingsDraft.LogHealthColor = !m.settingsDraft.LogHealthColor
	case "Show deltas":
		m.settingsDraft.ShowDeltas = !m.settingsDraft.ShowDeltas
	case "Stats refresh":
		intervals := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second}
		current := 1
		for i, interval := range intervals {
			if m.settingsDraft.StatsRefresh == interval {
				current = i
				break
			}
		}
		m.settingsDraft.StatsRefresh = intervals[modIndex(current+direction, len(intervals))]
	case "Default pane":
		m.settingsDraft.DefaultActivity = activityMode(modIndex(int(m.settingsDraft.DefaultActivity)+direction, 3))
	case "Start in dashboard":
		m.settingsDraft.StartInDashboard = !m.settingsDraft.StartInDashboard
	case "Modal shadow":
		m.settingsDraft.ModalShadow = !m.settingsDraft.ModalShadow
	case "Vim mode":
		m.settingsDraft.CreateVim = !m.settingsDraft.CreateVim
	case "App log":
		m.settingsDraft.AppLog = appLogMode(modIndex(int(m.settingsDraft.AppLog)+direction, 3))
	case "AI provider":
		m.settingsDraft.AIProvider = aiProvider(modIndex(int(m.settingsDraft.AIProvider)+direction, 4))
	}
}

func (m *Model) saveSettingsDraft() {
	m.settings = m.settingsDraft
	setEditorVimMode(m.settings.CreateVim)
	m.saveSettings()
}

// appLogMaxLines caps the in-memory buffer so a long session doesn't grow
// it unbounded — oldest lines drop off first, same trim shape as other
// ring buffers in this codebase (e.g. stats history).
const appLogMaxLines = 500

// recordAppLog appends a line to the app log when the status bar just
// changed to a new, non-empty message — called once per Update from the
// wrapper above with the *previous* status/statusErr, so it only fires on
// an actual transition, not every tick the same status is held.
//
// Error status lines are captured in memory unconditionally, even with
// AppLog off — you don't know you'll want the log until after the error
// you're chasing has already happened, so gating errors behind an opt-in
// setting would defeat the point. off/on/save instead control the routine
// info-level noise (only kept once turned on) and disk persistence (save
// only) — see appLogMode.
func (m *Model) recordAppLog(prevStatus string, prevErr bool) {
	if m.status == "" || (m.status == prevStatus && m.statusErr == prevErr) {
		return
	}
	if !m.statusErr && m.settings.AppLog == appLogOff {
		return
	}
	level := "INFO"
	if m.statusErr {
		level = "ERROR"
	}
	line := fmt.Sprintf("%s  %-5s  %s", time.Now().Format("15:04:05"), level, m.status)
	m.appLogLines = append(m.appLogLines, line)
	if len(m.appLogLines) > appLogMaxLines {
		m.appLogLines = m.appLogLines[len(m.appLogLines)-appLogMaxLines:]
	}
	if m.settings.AppLog == appLogSave {
		m.writeAppLogLine(line)
	}
}

// writeAppLogLine appends one line to the on-disk app log, opening the file
// (once, lazily) the first time save mode actually has something to write.
// A failure to open it is silent rather than surfaced as a status error —
// logging a logging failure would just be noise, and the in-memory buffer
// (still populated regardless of save mode) remains available either way.
func (m *Model) writeAppLogLine(line string) {
	if m.appLogFile == nil {
		path := m.appLogFilePath()
		if path == "" {
			return
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		m.appLogFile = f
	}
	fmt.Fprintln(m.appLogFile, line)
}

// appLogFilePath is whatthedock.log next to settings.json — same directory
// as everything else this app persists, rather than introducing a new
// config location just for this.
func (m Model) appLogFilePath() string {
	if m.settingsPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(m.settingsPath), "whatthedock.log")
}

func (m *Model) saveSettings() {
	if m.settingsPath == "" {
		return
	}
	if err := config.SaveSettings(m.settingsPath, m.persistedSettings()); err != nil {
		m.status, m.statusErr = "settings: "+err.Error(), true
		return
	}
	if m.status == "settings reset to defaults" && !m.statusErr {
		return
	}
	m.status, m.statusErr = "settings saved", false
}

func (s graphStyle) String() string {
	switch s {
	case graphStyleBlocks:
		return "blocks"
	case graphStyleBraille:
		return "braille"
	case graphStyleBars:
		return "bars"
	case graphStyleGauge:
		return "gauge"
	default:
		return "wave"
	}
}

func (m graphColorMode) String() string {
	switch m {
	case graphColorMetric:
		return "metric"
	case graphColorMono:
		return "mono"
	default:
		return "gradient"
	}
}

func (m logColorMode) String() string {
	switch m {
	case logColorSeverity:
		return "severity"
	case logColorHTTP:
		return "http"
	case logColorMono:
		return "mono"
	default:
		return "full"
	}
}

func (a appLogMode) String() string {
	switch a {
	case appLogOn:
		return "on"
	case appLogSave:
		return "save"
	default:
		return "off"
	}
}

func (p aiProvider) String() string {
	switch p {
	case aiProviderOpenAI:
		return "openai"
	case aiProviderGemini:
		return "gemini"
	case aiProviderCustom:
		return "custom"
	default:
		return "anthropic"
	}
}

func (m *Model) openLogFilter() {
	m.overlay = overlayLogFilter
	m.logDraft = m.logFilter
}

func (m *Model) setLogSeverityFilter(filter logSeverityFilter) {
	m.logLevel = filter
	m.logMatch = 0
	m.clampLogScroll()
	m.saveLogViewState()
	m.status, m.statusErr = "log filter: "+filterStatus(m.logFilter, m.logLevel), false
}

func (m *Model) clearLogFilter() {
	m.logFilter = ""
	m.logDraft = ""
	m.logLevel = logSeverityAll
	m.logMatch = 0
	m.followLogs()
	m.status, m.statusErr = "log filter cleared", false
}

func (m *Model) jumpLogMatch(direction int) {
	matches := m.logMatchIndexes()
	if len(matches) == 0 {
		m.status, m.statusErr = "no log matches", false
		return
	}
	if direction == 0 {
		direction = 1
	}
	m.logMatch = modIndex(m.logMatch+direction, len(matches))
	m.logFollow = false
	m.logScroll = matches[m.logMatch]
	m.clampLogScroll()
	m.saveLogViewState()
	m.status, m.statusErr = fmt.Sprintf("match %d/%d", m.logMatch+1, len(matches)), false
}

func (m Model) logMatchIndexes() []int {
	query := strings.ToLower(strings.TrimSpace(m.logFilter))
	if query == "" {
		return nil
	}
	lines := m.visibleLogLines()
	matches := make([]int, 0, len(lines))
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), query) {
			matches = append(matches, i)
		}
	}
	return matches
}

func (m Model) logMatchStatus() (int, int) {
	matches := m.logMatchIndexes()
	if len(matches) == 0 {
		return 0, 0
	}
	return clamp(m.logMatch, 0, len(matches)-1) + 1, len(matches)
}

func (m *Model) scrollLogs(delta int) {
	lines := len(m.visibleLogLines())
	if lines == 0 {
		m.logScroll = 0
		m.logFollow = true
		m.saveLogViewState()
		return
	}
	visible := m.logVisibleRows()
	if m.logFollow {
		m.logScroll = max(0, lines-visible)
	}
	m.logFollow = false
	m.logScroll += delta
	m.clampLogScroll()
	m.saveLogViewState()
}

func (m *Model) followLogs() {
	m.logFollow = true
	m.clampLogScroll()
	m.saveLogViewState()
}

func (m *Model) clampLogScroll() {
	lines := len(m.visibleLogLines())
	if lines == 0 {
		m.logScroll = max(0, m.logScroll)
		return
	}
	visible := m.logVisibleRows()
	maxScroll := max(0, lines-visible)
	if m.logFollow {
		m.logScroll = maxScroll
		return
	}
	m.logScroll = clamp(m.logScroll, 0, maxScroll)
	if m.logScroll >= maxScroll {
		m.logFollow = true
	}
	if matchCount := len(m.logMatchIndexes()); matchCount > 0 {
		m.logMatch = clamp(m.logMatch, 0, matchCount-1)
	} else {
		m.logMatch = 0
	}
}

func (m Model) logVisibleRows() int {
	headerRows := 1
	return max(1, m.activityVisibleRows()-headerRows)
}

func (m Model) treeVisibleRows() int {
	return max(1, m.paneContentRows()-m.paneActionStripRows(paneTree))
}

func (m Model) activityVisibleRows() int {
	return max(1, m.paneContentRows()-m.paneActionStripRows(paneActivity))
}

// problemsInsightRows is the fixed row allowance for the rule-based insight
// block beneath the Problems list — enough for a few sentences without
// eating the whole pane, but never so much it leaves the list with nothing
// on a short terminal, hence the total-1-for-divider floor shared with
// problemsListRows below.
func (m Model) problemsInsightRows() int {
	total := m.activityVisibleRows()
	want := 6
	// A real AI response is meant to run several sentences (the prompt
	// itself asks for 3-6) — the plain rule-based insight's small fixed
	// budget was truncating it well before it naturally ended. Give it
	// room to actually fit instead, still bounded by what's left after the
	// list keeps its own minimum below.
	if m.aiInsightActive() {
		want = 40
	}
	if ceiling := total - 1 - 3; want > ceiling { // leave the list at least 3 rows
		want = ceiling
	}
	return max(2, want)
}

// aiInsightActive reports whether the Problems pane's insight block should
// currently be showing AI content (loading, a result, or an error) instead
// of the plain rule-based text — true exactly when there's AI state that
// actually belongs to whatever problem row is currently selected (see
// currentProblem/aiAnalysisFor), same guard problemInsightText applies.
func (m Model) aiInsightActive() bool {
	current := m.currentProblem(m.snapshotProblems())
	if current == nil || m.aiAnalysisFor != current.id {
		return false
	}
	return m.aiAnalyzing || m.aiAnalysisErr != nil || m.aiAnalysis != ""
}

// problemsListRows is what's left of the Problems pane for the scrollable
// problem list once the insight block and its divider are accounted for —
// same subtract-from-activityVisibleRows shape as logVisibleRows above.
func (m Model) problemsListRows() int {
	return max(3, m.activityVisibleRows()-1-m.problemsInsightRows())
}

func (m Model) treeVisibleStart() int {
	start, _ := visibleRange(len(m.rows), m.cursor, m.treeVisibleRows())
	return start
}

func (m *Model) saveLogViewState() {
	if m.logViews == nil {
		m.logViews = map[domain.ResourceID]logViewState{}
	}
	id := m.logViewID
	if id.ID == "" {
		id = m.selectedID
	}
	if id.ID == "" {
		return
	}
	m.logViews[id] = logViewState{
		filter:     m.logFilter,
		level:      m.logLevel,
		scroll:     m.logScroll,
		follow:     m.logFollow,
		matchIndex: m.logMatch,
	}
	m.logViewID = id
}

func (m *Model) restoreLogViewState(id domain.ResourceID) {
	if m.logViews == nil {
		m.logViews = map[domain.ResourceID]logViewState{}
	}
	draft := m.logDraft
	state, ok := m.logViews[id]
	if !ok {
		state = logViewState{follow: true}
	}
	m.logFilter = state.filter
	m.logDraft = state.filter
	if m.overlay == overlayLogFilter {
		m.logDraft = draft
	}
	m.logLevel = state.level
	m.logScroll = state.scroll
	m.logFollow = state.follow
	m.logMatch = state.matchIndex
	if !ok {
		m.logFollow = true
	}
	m.logViewID = id
	m.clampLogScroll()
}

func filterStatus(query string, level logSeverityFilter) string {
	query = strings.TrimSpace(query)
	status := level.String()
	if query != "" {
		status += " matching " + query
	}
	return status
}

func (f logSeverityFilter) String() string {
	switch f {
	case logSeverityErrors:
		return "errors"
	case logSeverityWarnings:
		return "warnings"
	case logSeverityInfo:
		return "info"
	default:
		return "all"
	}
}

func activityModeName(mode activityMode) string {
	switch mode {
	case activityLogs:
		return "logs"
	case activityStats:
		return "stats"
	default:
		return "problems"
	}
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func formatRefreshInterval(interval time.Duration) string {
	if interval <= 0 {
		interval = defaultSettings().StatsRefresh
	}
	if interval%time.Second == 0 {
		return fmt.Sprintf("%ds", int(interval/time.Second))
	}
	return interval.String()
}

func modIndex(value, size int) int {
	if size <= 0 {
		return 0
	}
	value %= size
	if value < 0 {
		value += size
	}
	return value
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if m.overlay == overlayDashboard {
		return m.handleDashboardMouse(msg)
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		switch {
		case m.focus == paneActivity && m.mode == activityProblems:
			m.moveProblemCursor(-1)
		case m.focus == paneActivity && m.mode == activityLogs:
			m.scrollLogs(-1)
		case m.focus == paneInspector:
			m.scrollInspector(-1)
		case m.focus == paneTree:
			return m, m.focusTreeIndex(m.cursor - 1)
		}
		return m, nil
	case tea.MouseButtonWheelDown:
		switch {
		case m.focus == paneActivity && m.mode == activityProblems:
			m.moveProblemCursor(1)
		case m.focus == paneInspector:
			m.scrollInspector(1)
		case m.focus == paneActivity && m.mode == activityLogs:
			m.scrollLogs(1)
		case m.focus == paneTree:
			return m, m.focusTreeIndex(m.cursor + 1)
		}
		return m, nil
	case tea.MouseButtonLeft:
		treeRowOffset := msg.Y - 3
		visibleTreeRows := min(len(m.rows), m.treeVisibleRows())
		if msg.X < m.leftPaneWidth() && treeRowOffset >= 0 && treeRowOffset < visibleTreeRows {
			m.focus = paneTree
			return m, m.focusTreeIndex(m.treeVisibleStart() + treeRowOffset)
		}
		if msg.X >= m.leftPaneWidth() && msg.X < m.leftPaneWidth()+m.centerPaneWidth() && m.mode == activityProblems {
			m.focus = paneActivity
			return m.selectProblem(msg.Y - 4)
		}
	}
	return m, nil
}

// handleDashboardMouse mirrors handleMouse's wheel-scroll/left-click
// shape for the Dashboard overlay specifically: wheel moves the row
// selection the same way it moves the tree cursor elsewhere, and a left
// click either selects-and-opens a container row (dashboardOpenSelected,
// reusing the same path Enter takes) or the bottom status row's "View
// problems" action (dashboardOpenProblems, the same path "p" takes) —
// see dashboardHitTest for how a click's screen coordinates map back to
// a row.
func (m Model) handleDashboardMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.dashboardMoveCursor(-1)
		return m, nil
	case tea.MouseButtonWheelDown:
		m.dashboardMoveCursor(1)
		return m, nil
	case tea.MouseButtonLeft:
		row, isStatusRow, ok := m.dashboardHitTest(msg)
		if !ok {
			return m, nil
		}
		if isStatusRow {
			return m.dashboardOpenProblems()
		}
		m.dashboardCursor = row
		return m.dashboardOpenSelected()
	}
	return m, nil
}

func (m *Model) scrollInspector(delta int) {
	m.inspectorScroll = max(0, m.inspectorScroll+delta)
}

func (m *Model) scrollHelp(delta int) {
	maxScroll := max(0, len(helpLines)-m.helpBodyBudget())
	m.helpScroll = clamp(m.helpScroll+delta, 0, maxScroll)
}

func (m *Model) scrollAppLog(delta int) {
	maxScroll := max(0, len(m.appLogLines)-m.appLogBodyBudget())
	m.appLogScroll = clamp(m.appLogScroll+delta, 0, maxScroll)
}

func (m Model) inspectorVisibleRows() int {
	return m.paneContentRows()
}

func (m Model) paneActionStripRows(pane pane) int {
	if m.focus != pane {
		return 0
	}
	if m.height < 10 {
		return 0
	}
	return 1
}

func (m Model) paneContentRows() int {
	return max(1, m.height-5)
}

func (m *Model) moveCursor(delta int) {
	_ = m.focusTreeIndex(m.cursor + delta)
}

func (m *Model) moveProblemCursor(delta int) {
	problems := m.snapshotProblems()
	if len(problems) == 0 {
		m.problemCursor = 0
		return
	}
	m.problemCursor = clamp(m.problemCursor+delta, 0, len(problems)-1)
}

func (m *Model) syncProblemCursor() {
	problems := m.snapshotProblems()
	if len(problems) == 0 {
		m.problemCursor = 0
		return
	}
	for i, problem := range problems {
		if problem.id == m.selectedID {
			m.problemCursor = i
			return
		}
	}
	m.problemCursor = clamp(m.problemCursor, 0, len(problems)-1)
}

func (m Model) selectProblem(index int) (tea.Model, tea.Cmd) {
	problems := m.snapshotProblems()
	if len(problems) == 0 {
		return m, nil
	}
	index = clamp(index, 0, len(problems)-1)
	m.problemCursor = index
	if !m.moveTreeCursorTo(problems[index].id) {
		return m, nil
	}
	m.focus = paneTree
	return m, m.loadSelectedCmd()
}

// dashboardMoveCursor moves the Dashboard overlay's row selection by
// delta, clamped to the overlay's own currently-visible container list
// (see dashboardBodyPlan) — mirrors moveProblemCursor's shape for the
// Problems pane.
func (m *Model) dashboardMoveCursor(delta int) {
	shown, _, _, _, _ := m.dashboardBodyPlan()
	if len(shown) == 0 {
		m.dashboardCursor = 0
		return
	}
	m.dashboardCursor = clamp(m.dashboardCursor+delta, 0, len(shown)-1)
}

// dashboardOpenSelected closes the Dashboard and focuses the inspector on
// the currently highlighted row's container — reusing moveTreeCursorTo/
// loadSelectedCmd, the same path selectProblem uses to jump from the
// Problems pane into the tree, rather than inventing a second "select a
// container" workflow.
func (m Model) dashboardOpenSelected() (tea.Model, tea.Cmd) {
	shown, _, _, _, _ := m.dashboardBodyPlan()
	if len(shown) == 0 {
		return m, nil
	}
	index := clamp(m.dashboardCursor, 0, len(shown)-1)
	if !m.moveTreeCursorTo(shown[index].ID) {
		return m, nil
	}
	m.overlay = overlayNone
	m.focus = paneInspector
	return m, m.loadSelectedCmd()
}

// dashboardOpenProblems closes the Dashboard and opens the Problems pane
// — the exact same focus/mode change the global "p" key performs
// (handleKey's own "p" case), just also reachable while the Dashboard
// overlay has the key/mouse focus.
func (m Model) dashboardOpenProblems() (tea.Model, tea.Cmd) {
	m.overlay = overlayNone
	m.focus = paneActivity
	m.mode = activityProblems
	m.syncProblemCursor()
	return m, nil
}

func (m *Model) moveTreeCursorTo(id domain.ResourceID) bool {
	for i, row := range m.rows {
		if row.container != nil && row.container.ID == id {
			m.cursor = i
			m.focusedTreeKey = row.key()
			m.selectedID = id
			return true
		}
	}
	return false
}

func (m Model) currentRow() *treeRow {
	if len(m.rows) == 0 || m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return &m.rows[m.cursor]
}

func (r treeRow) key() treeRowKey {
	if r.container != nil {
		return treeRowKey{
			valid:       true,
			kind:        rowContainer,
			containerID: r.container.ID,
		}
	}
	return treeRowKey{
		valid:   true,
		kind:    r.kind,
		label:   r.label,
		project: r.project,
		service: r.service,
	}
}

func (m *Model) syncFocusedTreeKey() {
	if row := m.currentRow(); row != nil {
		m.focusedTreeKey = row.key()
		return
	}
	m.focusedTreeKey = treeRowKey{}
}

func (m *Model) focusTreeIndex(index int) tea.Cmd {
	if len(m.rows) == 0 {
		m.cursor = 0
		m.focusedTreeKey = treeRowKey{}
		m.clearSelectedContainer()
		return nil
	}
	m.cursor = clamp(index, 0, len(m.rows)-1)
	m.syncFocusedTreeKey()
	return m.applyFocusedTreeRow()
}

func (m *Model) applyFocusedTreeRow() tea.Cmd {
	row := m.currentRow()
	if row == nil || row.container == nil {
		m.clearSelectedContainer()
		return nil
	}
	m.selectedID = row.container.ID
	return m.loadSelectedCmd()
}

func (m *Model) clearSelectedContainer() {
	if m.selectedID.ID != "" {
		m.saveLogViewState()
	}
	m.selectedID = domain.ResourceID{}
	m.selected = nil
	m.stats = nil
	m.statsID = domain.ResourceID{}
	m.statsLoading = false
	m.statsErr = nil
	m.inspectorScroll = 0
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
	}
	m.logChan = nil
	m.logLines = nil
	m.logErr = nil
	m.logReplaceOnDrain = false
	m.logLoading = false
}

func (m *Model) restoreFocusedTreeRow(previousCursor int, selectInitial bool) tea.Cmd {
	if len(m.rows) == 0 {
		m.cursor = 0
		m.focusedTreeKey = treeRowKey{}
		m.clearSelectedContainer()
		return nil
	}
	if m.focusedTreeKey.valid && m.moveTreeCursorToKey(m.focusedTreeKey) {
		return m.applyFocusedTreeRow()
	}
	if !m.focusedTreeKey.valid && selectInitial {
		if m.selectFirstContainer() {
			return m.loadSelectedCmd()
		}
	}
	m.cursor = clamp(previousCursor, 0, len(m.rows)-1)
	m.syncFocusedTreeKey()
	m.clearSelectedContainer()
	return nil
}

func (m *Model) moveTreeCursorToKey(key treeRowKey) bool {
	if !key.valid {
		return false
	}
	for i, row := range m.rows {
		if row.key() == key {
			m.cursor = i
			m.focusedTreeKey = key
			return true
		}
	}
	return false
}

func (m *Model) selectFirstContainer() bool {
	for i, row := range m.rows {
		if row.container != nil {
			m.cursor = i
			m.focusedTreeKey = row.key()
			m.selectedID = row.container.ID
			return true
		}
	}
	m.cursor = clamp(m.cursor, 0, len(m.rows)-1)
	m.syncFocusedTreeKey()
	m.clearSelectedContainer()
	return false
}

func (m Model) selectedContainer() *domain.Container {
	if row := m.currentRow(); row != nil && row.container != nil {
		return row.container
	}
	if m.selectedID.ID != "" {
		return m.selected
	}
	return nil
}

func (m Model) buildRows() []treeRow {
	query := strings.ToLower(strings.TrimSpace(m.filter))
	matches := func(ctr domain.Container) bool {
		if query == "" {
			return true
		}
		return strings.Contains(ctr.SearchText(), query)
	}
	var rows []treeRow
	for _, project := range m.snapshot.Projects {
		projectRows := []treeRow{}
		for _, service := range project.Services {
			serviceRows := []treeRow{}
			for _, ctr := range service.Containers {
				if matches(ctr) {
					c := ctr
					serviceRows = append(serviceRows, treeRow{kind: rowContainer, label: ctr.DisplayName(), project: project.Name, service: service.Name, container: &c, depth: 2})
				}
			}
			if len(serviceRows) > 0 {
				projectRows = append(projectRows, treeRow{kind: rowService, label: service.Name, project: project.Name, service: service.Name, depth: 1, muted: true})
				projectRows = append(projectRows, serviceRows...)
			}
		}
		if len(projectRows) > 0 || query == "" {
			rows = append(rows, treeRow{kind: rowProject, label: project.Name, project: project.Name})
			if !m.collapsed[project.Name] {
				rows = append(rows, projectRows...)
			}
		}
	}
	if len(m.snapshot.Standalone) > 0 {
		sectionRows := []treeRow{}
		for _, ctr := range m.snapshot.Standalone {
			if matches(ctr) {
				c := ctr
				sectionRows = append(sectionRows, treeRow{kind: rowContainer, label: ctr.DisplayName(), container: &c, depth: 1})
			}
		}
		if len(sectionRows) > 0 || query == "" {
			rows = append(rows, treeRow{kind: rowSection, label: "Standalone containers", muted: true})
			rows = append(rows, sectionRows...)
		}
	}
	return rows
}

func (m Model) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		snapshot, err := m.provider.Snapshot(ctx)
		return snapshotMsg{snapshot: snapshot, err: err}
	}
}

func (m *Model) loadSelectedCmd() tea.Cmd {
	selected := m.selectedContainer()
	if selected == nil {
		return nil
	}
	id := selected.ID
	m.selectedID = id
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ctr, err := provider.Container(ctx, id)
		return detailMsg{id: id, container: ctr, err: err}
	}
}

func (m Model) loadStatsCmd(id domain.ResourceID) tea.Cmd {
	if id.ID == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		stats, err := m.provider.ContainerStats(ctx, id)
		if stats.ID.ID == "" {
			stats.ID = id
		}
		return statsMsg{stats: stats, err: err}
	}
}

func (m Model) nextStatsTickCmd(id domain.ResourceID) tea.Cmd {
	if m.mode != activityStats || id.ID == "" || id != m.selectedID {
		return nil
	}
	interval := m.settings.StatsRefresh
	if interval <= 0 {
		interval = defaultSettings().StatsRefresh
	}
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return statsTickMsg{id: id}
	})
}

// dashboardRefreshCmd fans out one ContainerStats fetch per currently
// running container (stopped/dead ones have nothing to graph) and re-arms
// itself on a timer — the Dashboard overlay's own polling loop, kept
// independent of loadStatsCmd/nextStatsTickCmd's single-selected-container
// loop above (see dashboardStatsMsg/dashboardTickMsg). Returns nil once the
// overlay's been closed, so a stray tick that fires just after Esc doesn't
// keep dispatching fetches for a screen nobody's looking at.
func (m Model) dashboardRefreshCmd() tea.Cmd {
	if m.overlay != overlayDashboard {
		return nil
	}
	provider := m.provider
	var cmds []tea.Cmd
	for _, ctr := range m.snapshotContainers() {
		if !ctr.IsRunning() {
			continue
		}
		id := ctr.ID
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			stats, err := provider.ContainerStats(ctx, id)
			if stats.ID.ID == "" {
				stats.ID = id
			}
			return dashboardStatsMsg{stats: stats, err: err}
		})
	}
	cmds = append(cmds, dashboardTickCmd(m.settings.StatsRefresh))
	return tea.Batch(cmds...)
}

func dashboardTickCmd(interval time.Duration) tea.Cmd {
	if interval <= 0 {
		interval = defaultSettings().StatsRefresh
	}
	return tea.Tick(interval, func(time.Time) tea.Msg { return dashboardTickMsg{} })
}

func (m *Model) appendStats(stats domain.ContainerStats) {
	if m.statsHistory == nil {
		m.statsHistory = map[domain.ResourceID]statsHistory{}
	}
	history := m.statsHistory[stats.ID]
	history.CPU = appendFloatHistory(history.CPU, stats.CPUPercent, 24)
	history.Memory = appendUintHistory(history.Memory, stats.MemoryUsage, 24)
	if history.lastStats != nil {
		history.NetworkRx = appendUintHistory(history.NetworkRx, counterDelta(history.lastStats.NetworkRx, stats.NetworkRx), 24)
		history.NetworkTx = appendUintHistory(history.NetworkTx, counterDelta(history.lastStats.NetworkTx, stats.NetworkTx), 24)
		history.BlockTotal = appendUintHistory(history.BlockTotal, counterDelta(history.lastStats.BlockRead+history.lastStats.BlockWrite, stats.BlockRead+stats.BlockWrite), 24)
	}
	history.PIDs = appendUintHistory(history.PIDs, stats.PIDs, 24)
	history.maxCPU = maxFloat(history.maxCPU, stats.CPUPercent)
	history.maxMemory = maxUint(history.maxMemory, stats.MemoryUsage)
	if len(history.NetworkRx) > 0 {
		history.maxNetwork = maxUint(history.maxNetwork, maxUint(history.NetworkRx[len(history.NetworkRx)-1], history.NetworkTx[len(history.NetworkTx)-1]))
	}
	if len(history.BlockTotal) > 0 {
		history.maxBlock = maxUint(history.maxBlock, history.BlockTotal[len(history.BlockTotal)-1])
	}
	history.maxPIDs = maxUint(history.maxPIDs, stats.PIDs)
	copied := stats
	history.lastStats = &copied
	m.statsHistory[stats.ID] = history
}

func counterDelta(previous, current uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
}

func appendFloatHistory(values []float64, value float64, limit int) []float64 {
	values = append(values, value)
	if len(values) > limit {
		values = values[len(values)-limit:]
	}
	return values
}

func appendUintHistory(values []uint64, value uint64, limit int) []uint64 {
	values = append(values, value)
	if len(values) > limit {
		values = values[len(values)-limit:]
	}
	return values
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func maxUint(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func (m Model) actionCmd(id actions.ID, label string) tea.Cmd {
	selected := m.selectedContainer()
	if selected == nil {
		return nil
	}
	var command *actions.Command
	for _, item := range actions.Catalog(selected) {
		if item.ID == id {
			itemCopy := item
			command = &itemCopy
			break
		}
	}
	if command == nil || command.Run == nil || !command.Enabled {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := command.Run(ctx, m.provider, selected)
		return actionDoneMsg{label: label, err: err}
	}
}

func (m Model) startEventsCmd() tea.Cmd {
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		events, err := provider.Events(ctx)
		if err != nil {
			cancel()
			return eventsStartedMsg{err: err}
		}
		return eventsStartedMsg{events: events, cancel: cancel}
	}
}

func (m *Model) advanceEventBackoff() {
	if m.eventBackoff <= 0 {
		m.eventBackoff = time.Second
	} else {
		m.eventBackoff *= 2
		if m.eventBackoff > 30*time.Second {
			m.eventBackoff = 30 * time.Second
		}
	}
}

func (m Model) eventsReconnectCmd() tea.Cmd {
	backoff := m.eventBackoff
	if backoff <= 0 {
		backoff = time.Second
	}
	return tea.Tick(backoff, func(time.Time) tea.Msg { return eventsReconnectTickMsg{} })
}

func eventRefreshTickCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return eventRefreshTickMsg{} })
}

func waitForContainerEvent(events <-chan domain.ContainerEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return eventStreamClosedMsg{}
		}
		return containerEventMsg{event: event}
	}
}

func (m Model) startLogsCmd(id domain.ResourceID) tea.Cmd {
	if id.ID == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		stream, err := m.provider.Logs(ctx, id, app.LogOptions{Tail: "300", Follow: true})
		if err != nil {
			cancel()
			return logsStartedMsg{id: id, err: err}
		}
		lines := make(chan string, 256)
		go readLogLines(stream, lines, cancel)
		return logsStartedMsg{id: id, lines: lines, cancel: cancel}
	}
}

func tickLogs() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return logTickMsg{} })
}

func tickAbout() tea.Cmd {
	return tea.Tick(55*time.Millisecond, func(time.Time) tea.Msg { return aboutTickMsg{} })
}

// statusErrMinHold is how long an error status is guaranteed to stay on
// screen before a routine refresh (see the snapshotMsg case) is allowed to
// clear it back to "Docker connected" on its own.
const statusErrMinHold = 6 * time.Second

// tickStatusPulse drives the status bar's breathing connected-dot. Unlike
// tickAbout it isn't scoped to an overlay — it reschedules itself
// unconditionally for the life of the program, since the dot is part of
// the persistent status bar, not a transient screen.
func tickStatusPulse() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return statusPulseTickMsg{} })
}

func forwardLogs(in <-chan string, out chan<- string) {
	defer close(out)
	for line := range in {
		out <- line
	}
}

func readLogLines(reader io.ReadCloser, lines chan<- string, cancel context.CancelFunc) {
	defer cancel()
	defer close(lines)
	defer reader.Close()
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		lines <- cleanDockerLogLine(scanner.Text())
	}
}

func cleanDockerLogLine(line string) string {
	if len(line) > 8 && line[0] < ' ' {
		return line[8:]
	}
	return line
}

// drainReplicateProgress does a single non-blocking receive per call (not
// drain-to-empty like drainLogs) — progress is a "latest wins" display, not
// an append-only log, so grabbing at most one fresh line per
// statusPulseTickMsg tick is correct and cheaper.
func (m *Model) drainReplicateProgress() {
	if m.replicateProgress == nil {
		return
	}
	select {
	case line, ok := <-m.replicateProgress:
		if !ok {
			m.replicateProgress = nil
			return
		}
		m.status = line
	default:
	}
}

func (m *Model) drainLogs() {
	if m.logChan == nil {
		return
	}
	for {
		select {
		case line, ok := <-m.logChan:
			if !ok {
				m.logChan = nil
				m.logReplaceOnDrain = false
				return
			}
			if m.logReplaceOnDrain {
				m.logLines = nil
				m.logReplaceOnDrain = false
			}
			m.logLines = append(m.logLines, line)
			if len(m.logLines) > 1000 {
				m.logLines = m.logLines[len(m.logLines)-1000:]
			}
			if m.logFollow {
				m.clampLogScroll()
			}
		default:
			return
		}
	}
}

func (m *Model) cleanup() {
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
	}
	if m.eventCancel != nil {
		m.eventCancel()
		m.eventCancel = nil
	}
	_ = m.provider.Close()
}

func (m Model) filteredCommands() []actions.Command {
	selected := m.selectedContainer()
	query := strings.ToLower(strings.TrimSpace(m.commandFilter))
	items := actions.Catalog(selected)
	if query == "" {
		return items
	}
	var filtered []actions.Command
	for _, item := range items {
		haystack := strings.ToLower(item.Name + " " + string(item.ID) + " " + strings.Join(item.Aliases, " "))
		if strings.Contains(haystack, query) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func friendlyDockerError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Docker operation timed out"
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "permission denied"):
		return "Docker permission denied; check access to the Docker socket"
	case strings.Contains(text, "Cannot connect to the Docker daemon"), strings.Contains(text, "connection refused"):
		return "Docker is unavailable; start Docker and refresh"
	default:
		return text
	}
}

func clamp(value, minValue, maxValue int) int {
	if maxValue < minValue {
		return minValue
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func statusGlyph(ctr domain.Container) string {
	if ctr.Restarting || ctr.State == domain.StateRestarting {
		return "▲"
	}
	switch ctr.Health {
	case domain.HealthHealthy:
		return "●"
	case domain.HealthUnhealthy:
		return "!"
	}
	switch ctr.State {
	case domain.StateRunning:
		return "●"
	case domain.StateStopped, domain.StateExited:
		return "○"
	case domain.StateDead:
		return "✖"
	default:
		return "·"
	}
}

func statusText(ctr domain.Container) string {
	if ctr.Restarting || ctr.State == domain.StateRestarting {
		return "restarting"
	}
	if ctr.Health != "" {
		return string(ctr.Health)
	}
	return string(ctr.State)
}

func formatDuration(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t).Round(time.Second)
	if d < time.Minute {
		return d.String()
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 48*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

var _ tea.Model = Model{}
