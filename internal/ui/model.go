package ui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/allisonhere/tideui"
	osc52 "github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/whatthedock/internal/actions"
	"github.com/allisonhere/whatthedock/internal/app"
	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/domain"
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
)

type graphStyle int

const (
	graphStyleWave graphStyle = iota
	graphStyleBlocks
	graphStyleBraille
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
	GraphStyle      graphStyle
	GraphColor      graphColorMode
	LogColor        logColorMode
	ShowDeltas      bool
	StatsRefresh    time.Duration
	DefaultActivity activityMode
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
)

type settingsAction int

const (
	settingsActionNone settingsAction = iota
	settingsActionResetDefaults
)

type Model struct {
	provider app.Provider
	theme    tideui.Theme
	themes   tideui.ThemePicker

	width  int
	height int
	focus  pane
	mode   activityMode

	loading       bool
	snapshot      domain.Snapshot
	rows          []treeRow
	cursor        int
	problemCursor int
	collapsed     map[string]bool
	selectedID    domain.ResourceID
	selected      *domain.Container
	filter        string
	filterDraft   string

	logLines   []string
	logFilter  string
	logDraft   string
	logLevel   logSeverityFilter
	logScroll  int
	logFollow  bool
	logMatch   int
	logViews   map[domain.ResourceID]logViewState
	logViewID  domain.ResourceID
	logChan    chan string
	logCancel  context.CancelFunc
	logLoading bool
	logErr     error

	stats        *domain.ContainerStats
	statsID      domain.ResourceID
	statsHistory map[domain.ResourceID]statsHistory
	statsLoading bool
	statsErr     error

	status    string
	statusErr bool
	overlay   overlayMode

	commandFilter string
	commandCursor int

	settings       appSettings
	settingsCursor int
	settingsPath   string

	copyCursor int
	openCursor int
}

var clipboardWriter io.Writer = os.Stderr
var openTarget = defaultOpenTarget

type snapshotMsg struct {
	snapshot domain.Snapshot
	err      error
}

type detailMsg struct {
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
}

type actionDoneMsg struct {
	label string
	err   error
}

type logsStartedMsg struct {
	lines  <-chan string
	cancel context.CancelFunc
	err    error
}

type logTickMsg struct{}

type openDoneMsg struct {
	label string
	err   error
}

func NewModel(provider app.Provider) Model {
	return NewModelWithSettings(provider, config.Settings{}, "")
}

func NewModelWithSettings(provider app.Provider, persisted config.Settings, settingsPath string) Model {
	theme := whatthedockTheme()
	themes := append([]tideui.Theme{theme}, tideui.BuiltinThemes...)
	settings := defaultSettings()
	settings.applyPersisted(persisted)
	return Model{
		provider:     provider,
		theme:        theme,
		themes:       tideui.NewThemePicker(tideui.ThemePickerOptions{Themes: themes, InitialTheme: theme.Name, Title: "THEMES"}),
		mode:         settings.DefaultActivity,
		settings:     settings,
		settingsPath: settingsPath,
		statsHistory: map[domain.ResourceID]statsHistory{},
		logViews:     map[domain.ResourceID]logViewState{},
		logFollow:    true,
		collapsed:    map[string]bool{},
		status:       "connecting to Docker",
	}
}

func defaultSettings() appSettings {
	return appSettings{
		GraphStyle:      graphStyleWave,
		GraphColor:      graphColorGradient,
		LogColor:        logColorFull,
		ShowDeltas:      true,
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
	if persisted.ShowDeltas != nil {
		s.ShowDeltas = *persisted.ShowDeltas
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
}

func (s appSettings) persisted() config.Settings {
	showDeltas := s.ShowDeltas
	return config.Settings{
		GraphStyle:      s.GraphStyle.String(),
		GraphColor:      s.GraphColor.String(),
		LogColor:        s.LogColor.String(),
		ShowDeltas:      &showDeltas,
		StatsRefresh:    formatRefreshInterval(s.StatsRefresh),
		DefaultActivity: activityModeName(s.DefaultActivity),
	}
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
	return m.refreshCmd()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			m.selected = nil
			return m, nil
		}
		m.status, m.statusErr = "Docker connected", false
		m.snapshot = msg.snapshot
		m.rows = m.buildRows()
		m.preserveSelection()
		return m, m.loadSelectedCmd()
	case detailMsg:
		if msg.err != nil {
			m.status, m.statusErr = friendlyDockerError(msg.err), true
			return m, nil
		}
		m.saveLogViewState()
		selectionChanged := msg.container.ID != m.selectedID
		m.selected = &msg.container
		m.selectedID = msg.container.ID
		if selectionChanged {
			if m.logCancel != nil {
				m.logCancel()
				m.logCancel = nil
			}
			m.logChan = nil
			m.logLines = nil
			m.logErr = nil
		}
		m.restoreLogViewState(msg.container.ID)
		if m.mode == activityStats {
			m.statsLoading = true
			m.statsErr = nil
			return m, m.loadStatsCmd(msg.container.ID)
		}
		return m, m.startLogsCmd(msg.container.ID)
	case logsStartedMsg:
		if m.logCancel != nil {
			m.logCancel()
		}
		m.logLines = nil
		m.logErr = msg.err
		m.logLoading = false
		if msg.err != nil {
			m.status, m.statusErr = friendlyDockerError(msg.err), true
			return m, nil
		}
		m.logChan = make(chan string, 256)
		m.logCancel = msg.cancel
		go forwardLogs(msg.lines, m.logChan)
		return m, tickLogs()
	case logTickMsg:
		m.drainLogs()
		if m.logChan == nil {
			return m, nil
		}
		return m, tickLogs()
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
		m.statsLoading = m.stats == nil || m.stats.ID != msg.id
		m.statsErr = nil
		return m, m.loadStatsCmd(msg.id)
	case actionDoneMsg:
		if msg.err != nil {
			m.status, m.statusErr = msg.label+": "+friendlyDockerError(msg.err), true
		} else {
			m.status, m.statusErr = msg.label+" complete", false
		}
		return m, m.refreshCmd()
	case openDoneMsg:
		if msg.err != nil {
			m.status, m.statusErr = "open "+msg.label+": "+msg.err.Error(), true
		}
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
		if m.focus == paneActivity && m.mode == activityProblems {
			m.moveProblemCursor(1)
			return m, nil
		}
		if m.focus == paneActivity && m.mode == activityLogs {
			m.scrollLogs(1)
			return m, nil
		}
		m.moveCursor(1)
		return m, m.loadSelectedCmd()
	case "k", "up":
		if m.focus == paneActivity && m.mode == activityProblems {
			m.moveProblemCursor(-1)
			return m, nil
		}
		if m.focus == paneActivity && m.mode == activityLogs {
			m.scrollLogs(-1)
			return m, nil
		}
		m.moveCursor(-1)
		return m, m.loadSelectedCmd()
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
		if m.mode == activityLogs {
			m.focus = paneActivity
			if strings.TrimSpace(m.logFilter) == "" {
				m.openLogFilter()
				return m, nil
			}
			m.jumpLogMatch(1)
			return m, nil
		}
	case "N":
		if m.mode == activityLogs && strings.TrimSpace(m.logFilter) != "" {
			m.focus = paneActivity
			m.jumpLogMatch(-1)
			return m, nil
		}
	case " ":
		if row := m.currentRow(); row != nil && row.kind == rowProject {
			m.collapsed[row.project] = !m.collapsed[row.project]
			m.rows = m.buildRows()
			m.cursor = clamp(m.cursor, 0, len(m.rows)-1)
		}
	case "enter":
		if m.focus == paneActivity && m.mode == activityProblems {
			return m.selectProblem(m.problemCursor)
		}
		if row := m.currentRow(); row != nil {
			if row.kind == rowProject {
				m.collapsed[row.project] = !m.collapsed[row.project]
				m.rows = m.buildRows()
				m.cursor = clamp(m.cursor, 0, len(m.rows)-1)
				return m, nil
			}
			if row.container != nil {
				return m, m.loadSelectedCmd()
			}
		}
	case "/":
		if m.focus == paneActivity && m.mode == activityLogs {
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
	case "e":
		if m.focus == paneActivity && m.mode == activityLogs {
			m.setLogSeverityFilter(logSeverityErrors)
			return m, nil
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
	case "T":
		m.openThemePicker()
	case ",", "ctrl+,":
		m.overlay = overlaySettings
		m.settingsCursor = m.firstSettingsRow()
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
			m.rows = m.buildRows()
			m.cursor = clamp(m.cursor, 0, len(m.rows)-1)
			return m, m.loadSelectedCmd()
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
		if msg.String() == "esc" || msg.String() == "q" || msg.String() == "?" {
			m.overlay = overlayNone
		}
	case overlayCommandPalette:
		return m.handleCommandPaletteKey(msg)
	case overlayThemePicker:
		switch m.themes.Update(msg) {
		case tideui.ThemePickerConfirm:
			m.theme = m.themes.ConfirmedTheme()
			m.status, m.statusErr = "theme: "+m.theme.Name, false
			m.overlay = overlayNone
		case tideui.ThemePickerCancel:
			m.theme = m.themes.ConfirmedTheme()
			m.overlay = overlayNone
		default:
			m.theme = m.themes.PreviewTheme()
		}
	case overlaySettings:
		return m.handleSettingsKey(msg)
	case overlayCopy:
		return m.handleCopyKey(msg)
	case overlayOpen:
		return m.handleOpenKey(msg)
	}
	return m, nil
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
	switch msg.String() {
	case "esc", "q", ",", "ctrl+,":
		m.overlay = overlayNone
	case "up", "k":
		m.moveSettingsCursor(-1)
	case "down", "j", "tab":
		m.moveSettingsCursor(1)
	case "left", "h":
		m.cycleSetting(m.settingsCursor, -1)
		m.saveSettings()
	case "right", "l", "enter", " ":
		m.cycleSetting(m.settingsCursor, 1)
		m.saveSettings()
	}
	return m, nil
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
	case actions.StartStop:
		return m, m.actionCmd(actions.StartStop, "start/stop")
	case actions.Restart:
		return m, m.actionCmd(actions.Restart, "restart")
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
	case actions.OpenTheme:
		m.openThemePicker()
	case actions.OpenSettings:
		m.overlay = overlaySettings
		m.settingsCursor = m.firstSettingsRow()
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

func (m *Model) openThemePicker() {
	m.themes.Open(m.theme.Name)
	m.overlay = overlayThemePicker
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
	return []settingsRow{
		{label: "Stats", kind: settingsRowSection},
		{label: "Graph style", value: m.settings.GraphStyle.String()},
		{label: "Graph color", value: m.settings.GraphColor.String()},
		{label: "Show deltas", value: onOff(m.settings.ShowDeltas)},
		{label: "Stats refresh", value: formatRefreshInterval(m.settings.StatsRefresh)},
		{label: "Logs", kind: settingsRowSection},
		{label: "Log color", value: m.settings.LogColor.String()},
		{label: "Behavior", kind: settingsRowSection},
		{label: "Default pane", value: activityModeName(m.settings.DefaultActivity)},
		{label: "Maintenance", kind: settingsRowSection},
		{label: "Reset defaults", value: "apply", kind: settingsRowAction, action: settingsActionResetDefaults},
	}
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
		m.settings = defaultSettings()
		m.settingsCursor = clamp(index, 0, len(m.settingsRows())-1)
		m.status, m.statusErr = "settings reset to defaults", false
		return
	}
	switch row.label {
	case "Graph style":
		m.settings.GraphStyle = graphStyle(modIndex(int(m.settings.GraphStyle)+direction, 3))
	case "Graph color":
		m.settings.GraphColor = graphColorMode(modIndex(int(m.settings.GraphColor)+direction, 3))
	case "Log color":
		m.settings.LogColor = logColorMode(modIndex(int(m.settings.LogColor)+direction, 4))
	case "Show deltas":
		m.settings.ShowDeltas = !m.settings.ShowDeltas
	case "Stats refresh":
		intervals := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second}
		current := 1
		for i, interval := range intervals {
			if m.settings.StatsRefresh == interval {
				current = i
				break
			}
		}
		m.settings.StatsRefresh = intervals[modIndex(current+direction, len(intervals))]
	case "Default pane":
		m.settings.DefaultActivity = activityMode(modIndex(int(m.settings.DefaultActivity)+direction, 3))
	}
}

func (m *Model) saveSettings() {
	if m.settingsPath == "" {
		return
	}
	if err := config.SaveSettings(m.settingsPath, m.settings.persisted()); err != nil {
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
	return max(1, m.height-6)
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
	state, ok := m.logViews[id]
	if !ok {
		state = logViewState{follow: true}
	}
	m.logFilter = state.filter
	m.logDraft = state.filter
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
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if m.focus == paneActivity && m.mode == activityProblems {
			m.moveProblemCursor(-1)
			return m, nil
		}
		m.moveCursor(-1)
		return m, m.loadSelectedCmd()
	case tea.MouseButtonWheelDown:
		if m.focus == paneActivity && m.mode == activityProblems {
			m.moveProblemCursor(1)
			return m, nil
		}
		m.moveCursor(1)
		return m, m.loadSelectedCmd()
	case tea.MouseButtonLeft:
		if msg.X < m.leftPaneWidth() && msg.Y > 1 && msg.Y <= len(m.rows)+2 {
			m.cursor = clamp(msg.Y-3, 0, len(m.rows)-1)
			m.focus = paneTree
			return m, m.loadSelectedCmd()
		}
		if msg.X >= m.leftPaneWidth() && msg.X < m.leftPaneWidth()+m.centerPaneWidth() && m.mode == activityProblems {
			m.focus = paneActivity
			return m.selectProblem(msg.Y - 4)
		}
	}
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	if len(m.rows) == 0 {
		m.cursor = 0
		return
	}
	m.cursor = clamp(m.cursor+delta, 0, len(m.rows)-1)
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

func (m *Model) moveTreeCursorTo(id domain.ResourceID) bool {
	for i, row := range m.rows {
		if row.container != nil && row.container.ID == id {
			m.cursor = i
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

func (m *Model) preserveSelection() {
	m.rows = m.buildRows()
	if len(m.rows) == 0 {
		m.cursor = 0
		m.selected = nil
		return
	}
	if m.selectedID.ID != "" {
		for i, row := range m.rows {
			if row.container != nil && row.container.ID == m.selectedID {
				m.cursor = i
				return
			}
		}
	}
	if m.mode == activityProblems {
		for _, problem := range m.snapshotProblems() {
			if m.moveTreeCursorTo(problem.id) {
				m.syncProblemCursor()
				return
			}
		}
	}
	for i, row := range m.rows {
		if row.container != nil {
			m.cursor = i
			m.selectedID = row.container.ID
			return
		}
	}
	m.cursor = clamp(m.cursor, 0, len(m.rows)-1)
}

func (m Model) selectedContainer() *domain.Container {
	if row := m.currentRow(); row != nil && row.container != nil {
		return row.container
	}
	return m.selected
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

func (m Model) loadSelectedCmd() tea.Cmd {
	selected := m.selectedContainer()
	if selected == nil {
		return nil
	}
	id := selected.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ctr, err := m.provider.Container(ctx, id)
		return detailMsg{container: ctr, err: err}
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

func (m *Model) appendStats(stats domain.ContainerStats) {
	if m.statsHistory == nil {
		m.statsHistory = map[domain.ResourceID]statsHistory{}
	}
	history := m.statsHistory[stats.ID]
	history.CPU = appendFloatHistory(history.CPU, stats.CPUPercent, 24)
	history.Memory = appendUintHistory(history.Memory, stats.MemoryUsage, 24)
	history.NetworkRx = appendUintHistory(history.NetworkRx, stats.NetworkRx, 24)
	history.NetworkTx = appendUintHistory(history.NetworkTx, stats.NetworkTx, 24)
	history.BlockTotal = appendUintHistory(history.BlockTotal, stats.BlockRead+stats.BlockWrite, 24)
	history.PIDs = appendUintHistory(history.PIDs, stats.PIDs, 24)
	history.maxCPU = maxFloat(history.maxCPU, stats.CPUPercent)
	history.maxMemory = maxUint(history.maxMemory, stats.MemoryUsage)
	history.maxNetwork = maxUint(history.maxNetwork, maxUint(stats.NetworkRx, stats.NetworkTx))
	history.maxBlock = maxUint(history.maxBlock, stats.BlockRead+stats.BlockWrite)
	history.maxPIDs = maxUint(history.maxPIDs, stats.PIDs)
	m.statsHistory[stats.ID] = history
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

func (m Model) startLogsCmd(id domain.ResourceID) tea.Cmd {
	if id.ID == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		stream, err := m.provider.Logs(ctx, id, app.LogOptions{Tail: "300", Follow: true})
		if err != nil {
			cancel()
			return logsStartedMsg{err: err}
		}
		lines := make(chan string, 256)
		go readLogLines(stream, lines, cancel)
		return logsStartedMsg{lines: lines, cancel: cancel}
	}
}

func tickLogs() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return logTickMsg{} })
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

func (m *Model) drainLogs() {
	if m.logChan == nil {
		return
	}
	for {
		select {
		case line, ok := <-m.logChan:
			if !ok {
				m.logChan = nil
				return
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
