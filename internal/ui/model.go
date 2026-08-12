package ui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/allisonhere/tideui"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/allisonhere/tidedock/internal/actions"
	"github.com/allisonhere/tidedock/internal/app"
	"github.com/allisonhere/tidedock/internal/domain"
)

type pane int

const (
	paneTree pane = iota
	paneActivity
	paneInspector
)

type overlayMode int

const (
	overlayNone overlayMode = iota
	overlayHelp
	overlayFilter
	overlayCommandPalette
	overlayThemePicker
)

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

type Model struct {
	provider app.Provider
	theme    tideui.Theme
	themes   tideui.ThemePicker

	width  int
	height int
	focus  pane

	loading     bool
	snapshot    domain.Snapshot
	rows        []treeRow
	cursor      int
	collapsed   map[string]bool
	selectedID  domain.ResourceID
	selected    *domain.Container
	filter      string
	filterDraft string

	logLines   []string
	logChan    chan string
	logCancel  context.CancelFunc
	logLoading bool
	logErr     error

	status    string
	statusErr bool
	overlay   overlayMode

	commandFilter string
	commandCursor int
}

type snapshotMsg struct {
	snapshot domain.Snapshot
	err      error
}

type detailMsg struct {
	container domain.Container
	err       error
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

func NewModel(provider app.Provider) Model {
	theme, _ := tideui.ThemeByName("nord")
	return Model{
		provider:  provider,
		theme:     theme,
		themes:    tideui.NewThemePicker(tideui.ThemePickerOptions{InitialTheme: theme.Name, Title: "THEMES"}),
		collapsed: map[string]bool{},
		status:    "connecting to Docker",
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
		m.selected = &msg.container
		m.selectedID = msg.container.ID
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
	case actionDoneMsg:
		if msg.err != nil {
			m.status, m.statusErr = msg.label+": "+friendlyDockerError(msg.err), true
		} else {
			m.status, m.statusErr = msg.label+" complete", false
		}
		return m, m.refreshCmd()
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
		m.moveCursor(1)
		return m, m.loadSelectedCmd()
	case "k", "up":
		m.moveCursor(-1)
		return m, m.loadSelectedCmd()
	case " ":
		if row := m.currentRow(); row != nil && row.kind == rowProject {
			m.collapsed[row.project] = !m.collapsed[row.project]
			m.rows = m.buildRows()
			m.cursor = clamp(m.cursor, 0, len(m.rows)-1)
		}
	case "enter":
		if row := m.currentRow(); row != nil && row.container != nil {
			return m, m.loadSelectedCmd()
		}
	case "/":
		m.overlay = overlayFilter
		m.filterDraft = m.filter
	case "?":
		m.overlay = overlayHelp
	case "T":
		m.openThemePicker()
	case "ctrl+k":
		m.overlay = overlayCommandPalette
		m.commandFilter = ""
		m.commandCursor = 0
	case "r":
		if msg.Alt {
			return m, m.actionCmd(actions.Restart, "restart")
		}
		return m, m.refreshCmd()
	case "s":
		return m, m.actionCmd(actions.StartStop, "start/stop")
	case "l":
		m.focus = paneActivity
		if m.selected != nil {
			return m, m.startLogsCmd(m.selected.ID)
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
		if m.selected != nil {
			return m, m.startLogsCmd(m.selected.ID)
		}
	case actions.OpenFilter:
		m.overlay = overlayFilter
		m.filterDraft = m.filter
	case actions.OpenHelp:
		m.overlay = overlayHelp
	case actions.OpenTheme:
		m.openThemePicker()
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

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.moveCursor(-1)
		return m, m.loadSelectedCmd()
	case tea.MouseButtonWheelDown:
		m.moveCursor(1)
		return m, m.loadSelectedCmd()
	case tea.MouseButtonLeft:
		if msg.X < m.leftPaneWidth() && msg.Y > 0 && msg.Y <= len(m.rows) {
			m.cursor = clamp(msg.Y-1, 0, len(m.rows)-1)
			m.focus = paneTree
			return m, m.loadSelectedCmd()
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
