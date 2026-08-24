package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/allisonhere/tideui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/whatthedock/internal/catalog"
	"github.com/allisonhere/whatthedock/internal/config"
	"github.com/allisonhere/whatthedock/internal/domain"
	"github.com/allisonhere/whatthedock/internal/systems"
)

type composeStackEntry struct {
	Project     string
	Services    []string
	SourcePaths []string
	SystemID    string
	SystemName  string
}

type composeLibraryOpenMsg struct {
	entry catalog.Entry
	err   error
}

type composeCatalogImportMsg struct {
	entry catalog.Entry
	err   error
}

type composeCatalogBrowseMsg struct {
	dir     string
	entries []createFileEntry
	err     error
}

var composeFilesCommand = runDockerComposeFiles
var composeHTTPClient = http.DefaultClient

func (m Model) openComposeCuration() (tea.Model, tea.Cmd) {
	m.overlay = overlayComposeCuration
	m.composeCuratorTab = composeCuratorRunning
	m.composeCuratorMode = composeCuratorList
	m.composeCuratorStatus = composeStatusAll
	m.composeCuratorCursor = 0
	m.composeCuratorFilter = ""
	m.composeCuratorMessage = ""
	m.composeCuratorErr = ""
	m.composeCuratorEdit = ""
	m.composeCuratorEditCursor = 0
	m.composeDeployConflicts = nil
	m.composeDeployPendingEntry = catalog.Entry{}
	m.composeDeployPendingPath = ""
	m.composeDeployPath = ""
	m.reloadComposeCuration()
	return m, nil
}

func (m *Model) reloadComposeCuration() {
	m.composeRunning = composeStacksFromSnapshot(m.snapshot, m.activeSystemConfig())
	m.composeCatalogEntries = nil
	if m.catalogDir != "" {
		if entries, err := catalog.Load(m.catalogDir); err == nil {
			m.composeCatalogEntries = entries
			for _, running := range m.composeRunning {
				if entry, ok := matchingCatalogEntry(entries, running.SourcePaths); ok {
					_ = catalog.TouchSeen(m.catalogDir, entry.ID)
				}
			}
			entries, _ := catalog.Load(m.catalogDir)
			m.composeCatalogEntries = entries
		} else {
			m.composeCuratorErr = "compose catalog: " + err.Error()
		}
	}
	m.composeCuratorCursor = clamp(m.composeCuratorCursor, 0, max(0, len(m.composeCuratorRows())-1))
}

func composeStacksFromSnapshot(snapshot domain.Snapshot, system config.System) []composeStackEntry {
	var out []composeStackEntry
	for _, project := range snapshot.Projects {
		paths := map[string]bool{}
		services := make([]string, 0, len(project.Services))
		for _, service := range project.Services {
			services = append(services, service.Name)
			for _, ctr := range service.Containers {
				for _, file := range splitComposeConfigFiles(ctr.Compose.ConfigFiles) {
					paths[file] = true
				}
			}
		}
		sourcePaths := make([]string, 0, len(paths))
		for file := range paths {
			sourcePaths = append(sourcePaths, file)
		}
		sort.Strings(sourcePaths)
		sort.Strings(services)
		if len(sourcePaths) == 0 {
			continue
		}
		out = append(out, composeStackEntry{
			Project:     project.Name,
			Services:    services,
			SourcePaths: sourcePaths,
			SystemID:    system.ID,
			SystemName:  system.Name,
		})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Project) < strings.ToLower(out[j].Project) })
	return out
}

func matchingCatalogEntry(entries []catalog.Entry, sourcePaths []string) (catalog.Entry, bool) {
	wanted := map[string]bool{}
	for _, file := range sourcePaths {
		wanted[file] = true
	}
	for _, entry := range entries {
		for _, file := range entry.SourcePaths {
			if wanted[file] {
				return entry, true
			}
		}
	}
	return catalog.Entry{}, false
}

func (m Model) composeCuratorRows() []any {
	filter := strings.ToLower(strings.TrimSpace(m.composeCuratorFilter))
	if m.composeCuratorTab == composeCuratorRunning {
		rows := make([]any, 0, len(m.composeRunning))
		for _, row := range m.composeRunning {
			if filter == "" || strings.Contains(strings.ToLower(row.Project+" "+strings.Join(row.Services, " ")+" "+strings.Join(row.SourcePaths, " ")), filter) {
				rows = append(rows, row)
			}
		}
		return rows
	}
	rows := make([]any, 0, len(m.composeCatalogEntries))
	for _, entry := range m.composeCatalogEntries {
		if !m.composeCatalogEntryMatchesStatus(entry) {
			continue
		}
		haystack := strings.ToLower(entry.Name + " " + entry.Note + " " + strings.Join(entry.Tags, " ") + " " + strings.Join(entry.SourcePaths, " "))
		if filter == "" || strings.Contains(haystack, filter) {
			rows = append(rows, entry)
		}
	}
	return rows
}

func (m Model) currentComposeRunning() (composeStackEntry, bool) {
	rows := m.composeCuratorRows()
	if len(rows) == 0 || m.composeCuratorTab != composeCuratorRunning {
		return composeStackEntry{}, false
	}
	row, ok := rows[clamp(m.composeCuratorCursor, 0, len(rows)-1)].(composeStackEntry)
	return row, ok
}

func (m Model) currentComposeCatalogEntry() (catalog.Entry, bool) {
	rows := m.composeCuratorRows()
	if len(rows) == 0 || m.composeCuratorTab != composeCuratorCatalog {
		return catalog.Entry{}, false
	}
	row, ok := rows[clamp(m.composeCuratorCursor, 0, len(rows)-1)].(catalog.Entry)
	return row, ok
}

func (m *Model) selectComposeCatalogEntry(id string) {
	rows := m.composeCuratorRows()
	for i, row := range rows {
		entry, ok := row.(catalog.Entry)
		if ok && entry.ID == id {
			m.composeCuratorCursor = i
			return
		}
	}
	m.composeCuratorCursor = clamp(m.composeCuratorCursor, 0, max(0, len(rows)-1))
}

func (m Model) handleComposeCurationKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.composeCuratorMode {
	case composeCuratorNote:
		return m.handleComposeCuratorNoteKey(msg)
	case composeCuratorTags:
		return m.handleComposeCuratorTagsKey(msg)
	case composeCuratorAddSource:
		return m.handleComposeCuratorAddSourceKey(msg)
	case composeCuratorBrowse:
		return m.handleComposeCuratorBrowseKey(msg)
	case composeCuratorPreview:
		return m.handleComposeCuratorPreviewKey(msg)
	case composeCuratorDeploy:
		return m.handleComposeCuratorDeployKey(msg)
	case composeCuratorConflict:
		return m.handleComposeCuratorConflictKey(msg)
	case composeCuratorEditFile:
		return m.handleComposeCuratorEditorKey(msg)
	case composeCuratorDelete:
		return m.handleComposeCuratorDeleteKey(msg)
	}
	switch msg.String() {
	case "esc", "q":
		m.overlay = overlayNone
	case "tab":
		if m.composeCuratorTab == composeCuratorRunning {
			m.composeCuratorTab = composeCuratorCatalog
		} else {
			m.composeCuratorTab = composeCuratorRunning
		}
		m.composeCuratorCursor = 0
	case "j", "down":
		m.composeCuratorCursor = clamp(m.composeCuratorCursor+1, 0, max(0, len(m.composeCuratorRows())-1))
	case "k", "up":
		m.composeCuratorCursor = clamp(m.composeCuratorCursor-1, 0, max(0, len(m.composeCuratorRows())-1))
	case "r":
		m.reloadComposeCuration()
	case "f":
		if m.composeCuratorTab == composeCuratorCatalog {
			m.composeCuratorStatus = (m.composeCuratorStatus + 1) % 7
			m.composeCuratorCursor = 0
		}
	case "backspace":
		if m.composeCuratorFilter != "" {
			runes := []rune(m.composeCuratorFilter)
			m.composeCuratorFilter = string(runes[:len(runes)-1])
			m.composeCuratorCursor = 0
		}
	case "ctrl+u":
		m.composeCuratorFilter = ""
		m.composeCuratorCursor = 0
	case "s":
		if m.composeCuratorTab == composeCuratorRunning {
			return m.saveCurrentComposeStack("")
		}
	case "m":
		if m.composeCuratorTab == composeCuratorRunning {
			return m.openCurrentRunningInLibrary()
		}
	case "M", "p":
		if entry, ok := m.currentComposeCatalogEntry(); ok {
			m.composeCuratorMode = composeCuratorDeploy
			m.composeDeployPath = defaultComposeDeployPath(entry, m.activeSystemConfig())
			m.composeCuratorEdit = m.composeDeployPath
			m.composeCuratorEditCursor = len([]rune(m.composeCuratorEdit))
		}
	case "e":
		if m.composeCuratorTab == composeCuratorRunning {
			return m.openCurrentRunningInLibrary()
		}
		if entry, ok := m.currentComposeCatalogEntry(); ok {
			return m.openComposeCatalogEditor(entry)
		}
	case "c":
		if m.composeCuratorTab == composeCuratorCatalog {
			return m.loadCurrentComposeCatalogEntry()
		}
	case "S":
		if m.composeCuratorTab == composeCuratorCatalog {
			return m.saveCurrentComposeCatalogAsDraft()
		}
	case "A":
		if m.composeCuratorTab == composeCuratorCatalog {
			m.composeCuratorMode = composeCuratorAddSource
			m.composeCuratorEdit = ""
			m.composeCuratorEditCursor = 0
			m.composeCuratorMessage = "paste a URL or compose file path"
			m.composeCuratorErr = ""
		}
	case "B":
		if m.composeCuratorTab == composeCuratorCatalog {
			return m.openComposeCatalogBrowser()
		}
	case "N":
		if m.composeCuratorTab == composeCuratorCatalog {
			return m.createBlankComposeCatalogDraft()
		}
	case "n":
		m.composeCuratorMode = composeCuratorNote
		m.composeCuratorEdit = ""
		if entry, ok := m.currentComposeCatalogEntry(); ok {
			m.composeCuratorEdit = entry.Note
		}
		m.composeCuratorEditCursor = len([]rune(m.composeCuratorEdit))
	case "t":
		if entry, ok := m.currentComposeCatalogEntry(); ok {
			m.composeCuratorMode = composeCuratorTags
			m.composeCuratorEdit = strings.Join(entry.Tags, ", ")
			m.composeCuratorEditCursor = len([]rune(m.composeCuratorEdit))
		}
	case "enter", "l":
		if m.composeCuratorTab == composeCuratorRunning {
			return m.openCurrentRunningInLibrary()
		}
		if m.composeCuratorTab == composeCuratorCatalog {
			m.composeCuratorMode = composeCuratorPreview
		}
	case "a":
		if entry, ok := m.currentComposeCatalogEntry(); ok {
			if err := catalog.SetArchived(m.catalogDir, entry.ID, !entry.Archived); err != nil {
				m.composeCuratorErr = err.Error()
			} else {
				m.composeCuratorMessage = archiveMessage(entry)
				m.reloadComposeCuration()
			}
		}
	case "D":
		if _, ok := m.currentComposeCatalogEntry(); ok {
			m.composeCuratorMode = composeCuratorDelete
		}
	default:
		if len(msg.Runes) > 0 {
			m.composeCuratorFilter += string(msg.Runes)
			m.composeCuratorCursor = 0
		}
	}
	return m, nil
}

func (m Model) handleComposeCuratorNoteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.composeCuratorMode = composeCuratorList
	case "enter":
		note := m.composeCuratorEdit
		m.composeCuratorMode = composeCuratorList
		if m.composeCuratorTab == composeCuratorRunning {
			return m.saveCurrentComposeStack(note)
		}
		if entry, ok := m.currentComposeCatalogEntry(); ok {
			if err := catalog.UpdateNote(m.catalogDir, entry.ID, note); err != nil {
				m.composeCuratorErr = err.Error()
			} else {
				m.composeCuratorMessage = "updated note for " + entry.Name
				m.reloadComposeCuration()
			}
		}
	case "left":
		m.composeCuratorEditCursor = max(0, m.composeCuratorEditCursor-1)
	case "right":
		m.composeCuratorEditCursor = min(len([]rune(m.composeCuratorEdit)), m.composeCuratorEditCursor+1)
	case "home", "ctrl+a":
		m.composeCuratorEditCursor = 0
	case "end", "ctrl+e":
		m.composeCuratorEditCursor = len([]rune(m.composeCuratorEdit))
	case "backspace":
		m.editComposeCuratorText(-1)
	case "delete":
		m.editComposeCuratorText(1)
	case "ctrl+u":
		m.composeCuratorEdit = ""
		m.composeCuratorEditCursor = 0
	default:
		if len(msg.Runes) > 0 {
			m.insertComposeCuratorText(string(msg.Runes))
		}
	}
	return m, nil
}

func (m Model) handleComposeCuratorTagsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.composeCuratorMode = composeCuratorList
	case "enter":
		entry, ok := m.currentComposeCatalogEntry()
		m.composeCuratorMode = composeCuratorList
		if !ok {
			return m, nil
		}
		if err := catalog.SetTags(m.catalogDir, entry.ID, []string{m.composeCuratorEdit}); err != nil {
			m.composeCuratorErr = err.Error()
		} else {
			m.composeCuratorMessage = "updated tags for " + entry.Name
			m.composeCuratorErr = ""
			m.reloadComposeCuration()
		}
	case "left":
		m.composeCuratorEditCursor = max(0, m.composeCuratorEditCursor-1)
	case "right":
		m.composeCuratorEditCursor = min(len([]rune(m.composeCuratorEdit)), m.composeCuratorEditCursor+1)
	case "home", "ctrl+a":
		m.composeCuratorEditCursor = 0
	case "end", "ctrl+e":
		m.composeCuratorEditCursor = len([]rune(m.composeCuratorEdit))
	case "backspace":
		m.editComposeCuratorText(-1)
	case "delete":
		m.editComposeCuratorText(1)
	case "ctrl+u":
		m.composeCuratorEdit = ""
		m.composeCuratorEditCursor = 0
	default:
		if len(msg.Runes) > 0 {
			m.insertComposeCuratorText(string(msg.Runes))
		}
	}
	return m, nil
}

func (m Model) handleComposeCuratorAddSourceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.composeCuratorMode = composeCuratorList
	case "enter":
		source := strings.TrimSpace(m.composeCuratorEdit)
		if source == "" {
			m.composeCuratorErr = "URL or path is required"
			return m, nil
		}
		m.composeCuratorMessage = "adding compose file..."
		m.composeCuratorErr = ""
		return m, composeCatalogImportCmd(m.catalogDir, m.activeSystemConfig(), source)
	case "left":
		m.composeCuratorEditCursor = max(0, m.composeCuratorEditCursor-1)
	case "right":
		m.composeCuratorEditCursor = min(len([]rune(m.composeCuratorEdit)), m.composeCuratorEditCursor+1)
	case "home", "ctrl+a":
		m.composeCuratorEditCursor = 0
	case "end", "ctrl+e":
		m.composeCuratorEditCursor = len([]rune(m.composeCuratorEdit))
	case "backspace":
		m.editComposeCuratorText(-1)
	case "delete":
		m.editComposeCuratorText(1)
	case "ctrl+u":
		m.composeCuratorEdit = ""
		m.composeCuratorEditCursor = 0
	default:
		if len(msg.Runes) > 0 {
			m.insertComposeCuratorText(string(msg.Runes))
		}
	}
	return m, nil
}

func (m Model) handleComposeCuratorBrowseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.composeCuratorMode = composeCuratorList
	case "up", "k":
		m.moveComposeBrowseCursor(-1)
	case "down", "j", "tab":
		m.moveComposeBrowseCursor(1)
	case "home":
		m.composeBrowseCursor = 0
	case "end":
		m.composeBrowseCursor = max(0, len(m.composeBrowseFiles)-1)
	case "backspace", "left", "h":
		parent := filepath.Dir(m.composeBrowseDir)
		if m.activeSystemConfig().Kind == "ssh" {
			parent = path.Dir(m.composeBrowseDir)
		}
		return m, m.browseComposeCatalogDir(parent)
	case "enter", "right", "l":
		if len(m.composeBrowseFiles) == 0 {
			return m, nil
		}
		entry := m.composeBrowseFiles[clamp(m.composeBrowseCursor, 0, len(m.composeBrowseFiles)-1)]
		if entry.Dir {
			return m, m.browseComposeCatalogDir(entry.Path)
		}
		m.composeCuratorMessage = "adding " + entry.Name + "..."
		m.composeCuratorErr = ""
		return m, composeCatalogImportCmd(m.catalogDir, m.activeSystemConfig(), entry.Path)
	}
	return m, nil
}

func (m Model) handleComposeCuratorPreviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.composeCuratorMode = composeCuratorList
	case "e":
		if entry, ok := m.currentComposeCatalogEntry(); ok {
			return m.openComposeCatalogEditor(entry)
		}
	case "c":
		return m.loadCurrentComposeCatalogEntry()
	case "M", "p":
		if entry, ok := m.currentComposeCatalogEntry(); ok {
			m.composeCuratorMode = composeCuratorDeploy
			m.composeDeployPath = defaultComposeDeployPath(entry, m.activeSystemConfig())
			m.composeCuratorEdit = m.composeDeployPath
			m.composeCuratorEditCursor = len([]rune(m.composeCuratorEdit))
		}
	case "S":
		return m.saveCurrentComposeCatalogAsDraft()
	}
	return m, nil
}

func (m Model) handleComposeCuratorDeployKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.composeCuratorMode = composeCuratorList
	case "enter":
		entry, ok := m.currentComposeCatalogEntry()
		if !ok {
			m.composeCuratorMode = composeCuratorList
			return m, nil
		}
		var err error
		m, err = m.prepareComposeDeploy(entry, m.composeCuratorEdit)
		if err != nil {
			m.composeCuratorErr = err.Error()
		}
	case "left":
		m.composeCuratorEditCursor = max(0, m.composeCuratorEditCursor-1)
	case "right":
		m.composeCuratorEditCursor = min(len([]rune(m.composeCuratorEdit)), m.composeCuratorEditCursor+1)
	case "home", "ctrl+a":
		m.composeCuratorEditCursor = 0
	case "end", "ctrl+e":
		m.composeCuratorEditCursor = len([]rune(m.composeCuratorEdit))
	case "backspace":
		m.editComposeCuratorText(-1)
	case "delete":
		m.editComposeCuratorText(1)
	case "ctrl+u":
		m.composeCuratorEdit = ""
		m.composeCuratorEditCursor = 0
	default:
		if len(msg.Runes) > 0 {
			m.insertComposeCuratorText(string(msg.Runes))
		}
	}
	return m, nil
}

func (m Model) handleComposeCuratorConflictKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "n", "q":
		m.composeCuratorMode = composeCuratorList
		m.composeDeployConflicts = nil
		m.composeDeployPendingEntry = catalog.Entry{}
		m.composeDeployPendingPath = ""
	case "y", "enter":
		if err := m.finishComposeDeploy(m.composeDeployPendingEntry, m.composeDeployPendingPath); err != nil {
			m.composeCuratorErr = err.Error()
		} else {
			m.composeCuratorMessage = "made live " + m.composeDeployPendingEntry.Name
			m.composeCuratorErr = ""
			m.reloadComposeCuration()
		}
		m.composeCuratorMode = composeCuratorList
		m.composeDeployConflicts = nil
		m.composeDeployPendingEntry = catalog.Entry{}
		m.composeDeployPendingPath = ""
	}
	return m, nil
}

func (m Model) handleComposeCuratorEditorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+s":
		m.saveComposeCatalogEditor()
		return m, nil
	case "esc":
		if !m.composeCuratorEditor.vimMode() {
			m.cancelComposeCatalogEditor()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.composeCuratorEditor, cmd = m.composeCuratorEditor.Update(msg)
	return m, cmd
}

func (m Model) handleComposeCuratorDeleteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "n", "q":
		m.composeCuratorMode = composeCuratorList
	case "y", "enter":
		entry, ok := m.currentComposeCatalogEntry()
		m.composeCuratorMode = composeCuratorList
		if !ok {
			return m, nil
		}
		if err := catalog.Delete(m.catalogDir, entry.ID); err != nil {
			m.composeCuratorErr = err.Error()
		} else {
			m.composeCuratorMessage = "deleted catalog entry " + entry.Name
			m.reloadComposeCuration()
		}
	}
	return m, nil
}

func (m *Model) insertComposeCuratorText(value string) {
	runes := []rune(m.composeCuratorEdit)
	cursor := clamp(m.composeCuratorEditCursor, 0, len(runes))
	merged := append([]rune{}, runes[:cursor]...)
	merged = append(merged, []rune(value)...)
	merged = append(merged, runes[cursor:]...)
	m.composeCuratorEdit = string(merged)
	m.composeCuratorEditCursor = cursor + len([]rune(value))
}

func (m *Model) editComposeCuratorText(direction int) {
	runes := []rune(m.composeCuratorEdit)
	cursor := clamp(m.composeCuratorEditCursor, 0, len(runes))
	switch {
	case direction < 0 && cursor > 0:
		runes = append(runes[:cursor-1], runes[cursor:]...)
		m.composeCuratorEditCursor--
	case direction > 0 && cursor < len(runes):
		runes = append(runes[:cursor], runes[cursor+1:]...)
	}
	m.composeCuratorEdit = string(runes)
}

func (m Model) saveCurrentComposeStack(note string) (tea.Model, tea.Cmd) {
	if m.catalogDir == "" {
		m.composeCuratorErr = "compose catalog unavailable: settings path is not configured"
		return m, nil
	}
	stack, ok := m.currentComposeRunning()
	if !ok {
		m.composeCuratorErr = "no running compose stack selected"
		return m, nil
	}
	files, err := m.captureComposeFiles(stack)
	if err != nil {
		m.composeCuratorErr = err.Error()
		return m, nil
	}
	entries, err := catalog.Load(m.catalogDir)
	if err != nil {
		m.composeCuratorErr = err.Error()
		return m, nil
	}
	var entry catalog.Entry
	if existing, ok := matchingCatalogEntry(entries, stack.SourcePaths); ok {
		entry, err = catalog.ReplaceStack(m.catalogDir, existing.ID, note, stack.SystemID, stack.SourcePaths, files)
	} else {
		entry, err = catalog.SaveStack(m.catalogDir, stack.Project, note, stack.SystemID, stack.SourcePaths, files)
	}
	if err != nil {
		m.composeCuratorErr = err.Error()
		return m, nil
	}
	m.composeCuratorMessage = "saved compose entry " + entry.Name
	m.composeCuratorErr = ""
	m.reloadComposeCuration()
	return m, nil
}

func (m Model) openCurrentRunningInLibrary() (tea.Model, tea.Cmd) {
	if m.catalogDir == "" {
		m.composeCuratorErr = "compose catalog unavailable: settings path is not configured"
		return m, nil
	}
	stack, ok := m.currentComposeRunning()
	if !ok {
		m.composeCuratorErr = "no running compose stack selected"
		return m, nil
	}
	entries, err := catalog.Load(m.catalogDir)
	if err != nil {
		m.composeCuratorErr = err.Error()
		return m, nil
	}
	entry, ok := matchingCatalogEntry(entries, stack.SourcePaths)
	if !ok {
		m.composeCuratorMessage = "opening " + stack.Project + " in library..."
		m.composeCuratorErr = ""
		return m, composeLibraryOpenCmd(m.catalogDir, m.activeSystemConfig(), stack)
	}
	entry, ok, err = findComposeCatalogEntry(m.catalogDir, entry.ID)
	if err != nil {
		m.composeCuratorErr = err.Error()
		return m, nil
	}
	if !ok {
		m.composeCuratorErr = "catalog entry disappeared before edit"
		return m, nil
	}
	m.reloadComposeCuration()
	m.composeCuratorTab = composeCuratorCatalog
	m.composeCuratorStatus = composeStatusAll
	m.selectComposeCatalogEntry(entry.ID)
	return m.openComposeCatalogEditor(entry)
}

func composeLibraryOpenCmd(catalogDir string, system config.System, stack composeStackEntry) tea.Cmd {
	return func() tea.Msg {
		files := make([]catalog.FileContent, 0, len(stack.SourcePaths))
		for i, sourcePath := range stack.SourcePaths {
			content, err := readComposeFile(context.Background(), system, sourcePath)
			if err != nil {
				return composeLibraryOpenMsg{err: fmt.Errorf("read compose file %s: %w", sourcePath, err)}
			}
			files = append(files, catalog.FileContent{
				Name:       composeCatalogFileName(sourcePath, i),
				SourcePath: sourcePath,
				Content:    content,
				Primary:    i == 0,
			})
		}
		entry, err := catalog.SaveStack(catalogDir, stack.Project, "", stack.SystemID, stack.SourcePaths, files)
		return composeLibraryOpenMsg{entry: entry, err: err}
	}
}

func (m Model) handleComposeLibraryOpenMsg(msg composeLibraryOpenMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.composeCuratorErr = msg.err.Error()
		return m, nil
	}
	if msg.entry.ID == "" {
		m.composeCuratorErr = "catalog entry was not created"
		return m, nil
	}
	m.reloadComposeCuration()
	m.composeCuratorTab = composeCuratorCatalog
	m.composeCuratorStatus = composeStatusAll
	m.selectComposeCatalogEntry(msg.entry.ID)
	m.composeCuratorMessage = "saved compose entry " + msg.entry.Name
	m.composeCuratorErr = ""
	return m.openComposeCatalogEditor(msg.entry)
}

func (m Model) captureComposeFiles(stack composeStackEntry) ([]catalog.FileContent, error) {
	system := m.activeSystemConfig()
	files := make([]catalog.FileContent, 0, len(stack.SourcePaths))
	for i, sourcePath := range stack.SourcePaths {
		content, err := readComposeFile(context.Background(), system, sourcePath)
		if err != nil {
			return nil, fmt.Errorf("read compose file %s: %w", sourcePath, err)
		}
		files = append(files, catalog.FileContent{
			Name:       composeCatalogFileName(sourcePath, i),
			SourcePath: sourcePath,
			Content:    content,
			Primary:    i == 0,
		})
	}
	return files, nil
}

func readComposeFile(ctx context.Context, system config.System, sourcePath string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if system.Kind == "ssh" {
		output, err := sshRun(ctx, system, "cat "+systems.ShellQuote(sourcePath), "")
		return string(output), err
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func composeCatalogFileName(sourcePath string, index int) string {
	name := filepath.Base(sourcePath)
	if name == "." || name == string(filepath.Separator) || strings.TrimSpace(name) == "" {
		return fmt.Sprintf("compose-%d.yml", index+1)
	}
	return name
}

func (m Model) loadCurrentComposeCatalogEntry() (tea.Model, tea.Cmd) {
	entry, ok := m.currentComposeCatalogEntry()
	if !ok {
		return m, nil
	}
	content, err := catalog.Read(m.catalogDir, entry.ID)
	if err != nil {
		m.composeCuratorErr = err.Error()
		return m, nil
	}
	draft := m.defaultCreateDraft()
	draft.Mode = createModeCompose
	draft.Project = entry.Name
	draft.ComposeFile = firstNonEmpty(entry.SourcePaths, "compose.yml")
	draft.OverrideRaw = content
	draft.OverrideRawSet = true
	draft.OverrideRawBase = true
	draft.OverrideLoaded = true
	draft.applyOverrideFieldsFromYAML(content)
	m.openCreateOverlayWithDraft(draft)
	m.status, m.statusErr = "loaded compose catalog entry "+entry.Name, false
	return m, nil
}

func (m Model) saveCurrentComposeCatalogAsDraft() (tea.Model, tea.Cmd) {
	entry, ok := m.currentComposeCatalogEntry()
	if !ok {
		m.composeCuratorErr = "no catalog entry selected"
		return m, nil
	}
	draft, err := catalog.DuplicateAsDraft(m.catalogDir, entry.ID)
	if err != nil {
		m.composeCuratorErr = err.Error()
		return m, nil
	}
	m.composeCuratorStatus = composeStatusAll
	m.reloadComposeCuration()
	m.selectComposeCatalogEntry(draft.ID)
	m.composeCuratorMessage = "saved draft " + draft.Name
	m.composeCuratorErr = ""
	return m, nil
}

func (m Model) createBlankComposeCatalogDraft() (tea.Model, tea.Cmd) {
	if m.catalogDir == "" {
		m.composeCuratorErr = "compose catalog unavailable: settings path is not configured"
		return m, nil
	}
	content := "services:\n  app:\n    image: nginx:latest\n"
	entry, err := createComposeCatalogDraft(m.catalogDir, "New Compose draft", "", "", content)
	if err != nil {
		m.composeCuratorErr = err.Error()
		return m, nil
	}
	m.reloadComposeCuration()
	m.composeCuratorTab = composeCuratorCatalog
	m.composeCuratorStatus = composeStatusAll
	m.selectComposeCatalogEntry(entry.ID)
	m.composeCuratorMessage = "created draft " + entry.Name
	m.composeCuratorErr = ""
	return m.openComposeCatalogEditor(entry)
}

func findComposeCatalogEntry(dir, id string) (catalog.Entry, bool, error) {
	entries, err := catalog.Load(dir)
	if err != nil {
		return catalog.Entry{}, false, err
	}
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true, nil
		}
	}
	return catalog.Entry{}, false, nil
}

func composeCatalogImportCmd(catalogDir string, system config.System, source string) tea.Cmd {
	return func() tea.Msg {
		entry, err := importComposeCatalogDraft(context.Background(), catalogDir, system, source)
		return composeCatalogImportMsg{entry: entry, err: err}
	}
}

func importComposeCatalogDraft(ctx context.Context, catalogDir string, system config.System, source string) (catalog.Entry, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return catalog.Entry{}, errors.New("URL or path is required")
	}
	content, err := readComposeCatalogSource(ctx, system, source)
	if err != nil {
		return catalog.Entry{}, err
	}
	if err := lintComposeYAML(content); err != nil {
		return catalog.Entry{}, err
	}
	return createComposeCatalogDraft(catalogDir, composeCatalogImportName(source), source, composeCatalogFileName(source, 0), content)
}

func readComposeCatalogSource(ctx context.Context, system config.System, source string) (string, error) {
	if u, err := neturl.Parse(source); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return "", err
		}
		resp, err := composeHTTPClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return "", fmt.Errorf("download %s: %s", source, resp.Status)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return readComposeFile(ctx, system, source)
}

func createComposeCatalogDraft(catalogDir, name, sourcePath, fileName, content string) (catalog.Entry, error) {
	entry, err := catalog.SaveStack(catalogDir, name, "", "", []string{sourcePath}, []catalog.FileContent{{
		Name:       fileName,
		SourcePath: sourcePath,
		Content:    content,
		Primary:    true,
	}})
	if err != nil {
		return catalog.Entry{}, err
	}
	if err := catalog.SetStatus(catalogDir, entry.ID, catalog.StatusDraft); err != nil {
		return catalog.Entry{}, err
	}
	entry.Status = catalog.StatusDraft
	return entry, nil
}

func composeCatalogImportName(source string) string {
	candidate := source
	if u, err := neturl.Parse(source); err == nil && u.Path != "" {
		candidate = u.Path
	}
	base := filepath.Base(candidate)
	ext := filepath.Ext(base)
	base = strings.TrimSuffix(base, ext)
	if base == "" || base == "." || strings.EqualFold(base, "compose") || strings.EqualFold(base, "docker-compose") {
		parent := filepath.Base(filepath.Dir(candidate))
		if parent != "" && parent != "." && parent != string(filepath.Separator) {
			base = parent
		}
	}
	if strings.TrimSpace(base) == "" || base == "." || base == string(filepath.Separator) {
		return "Compose draft"
	}
	return base
}

func (m Model) handleComposeCatalogImportMsg(msg composeCatalogImportMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.composeCuratorErr = msg.err.Error()
		return m, nil
	}
	m.reloadComposeCuration()
	m.composeCuratorTab = composeCuratorCatalog
	m.composeCuratorStatus = composeStatusAll
	m.selectComposeCatalogEntry(msg.entry.ID)
	m.composeCuratorMessage = "added draft " + msg.entry.Name
	m.composeCuratorErr = ""
	return m.openComposeCatalogEditor(msg.entry)
}

func (m Model) openComposeCatalogBrowser() (tea.Model, tea.Cmd) {
	if m.catalogDir == "" {
		m.composeCuratorErr = "compose catalog unavailable: settings path is not configured"
		return m, nil
	}
	m.composeCuratorMode = composeCuratorBrowse
	m.composeBrowseErr = ""
	m.composeBrowseFiles = nil
	m.composeBrowseCursor = 0
	return m, m.browseComposeCatalogDir(createBrowserStartDir("", m.activeSystemConfig()))
}

func (m *Model) browseComposeCatalogDir(dir string) tea.Cmd {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	system := m.activeSystemConfig()
	if system.Kind == "ssh" {
		m.composeBrowseLoading = true
		m.composeBrowseErr = ""
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			entries, resolvedDir, err := remoteFileEntries(ctx, system, dir, "")
			return composeCatalogBrowseMsg{dir: resolvedDir, entries: entries, err: err}
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = filepath.Clean(dir)
	}
	entries, err := createFileEntries(abs, "")
	m.composeBrowseDir = abs
	m.composeBrowseCursor = 0
	m.composeBrowseFiles = entries
	m.composeBrowseErr = ""
	m.composeBrowseLoading = false
	if err != nil {
		m.composeBrowseErr = err.Error()
	}
	return nil
}

func (m Model) handleComposeCatalogBrowseMsg(msg composeCatalogBrowseMsg) (tea.Model, tea.Cmd) {
	m.composeBrowseLoading = false
	m.composeBrowseCursor = 0
	if msg.err != nil {
		m.composeBrowseErr = msg.err.Error()
		m.composeBrowseFiles = nil
		return m, nil
	}
	m.composeBrowseDir = msg.dir
	m.composeBrowseFiles = msg.entries
	m.composeBrowseErr = ""
	return m, nil
}

func (m *Model) moveComposeBrowseCursor(delta int) {
	if len(m.composeBrowseFiles) == 0 {
		m.composeBrowseCursor = 0
		return
	}
	m.composeBrowseCursor = clamp(m.composeBrowseCursor+delta, 0, len(m.composeBrowseFiles)-1)
}

func (m Model) openComposeCatalogEditor(entry catalog.Entry) (tea.Model, tea.Cmd) {
	content, err := catalog.Read(m.catalogDir, entry.ID)
	if err != nil {
		m.composeCuratorErr = err.Error()
		return m, nil
	}
	m.composeCuratorEditor = newEditorArea()
	m.composeCuratorEditor.SetValue(content)
	m.composeCuratorEditor.Focus()
	if m.composeCuratorEditor.vimMode() {
		m.composeCuratorEditor.EnterInsert()
	}
	m.composeCuratorMode = composeCuratorEditFile
	m.composeCuratorEditEntryID = entry.ID
	m.composeCuratorEditFile = firstNonEmpty([]string{entry.PrimaryFile}, "compose.yml")
	m.composeCuratorMessage = ""
	m.composeCuratorErr = ""
	return m, nil
}

func (m *Model) saveComposeCatalogEditor() {
	value := m.composeCuratorEditor.Value()
	if strings.TrimSpace(value) == "" {
		m.composeCuratorErr = "catalog entry content is empty"
		return
	}
	if err := lintComposeYAML(value); err != nil {
		m.composeCuratorErr = err.Error()
		return
	}
	if err := catalog.UpdatePrimaryFile(m.catalogDir, m.composeCuratorEditEntryID, value); err != nil {
		m.composeCuratorErr = err.Error()
		return
	}
	name := m.composeCuratorEditFile
	m.composeCuratorMode = composeCuratorList
	m.composeCuratorMessage = "updated " + name
	m.composeCuratorErr = ""
	m.composeCuratorEditEntryID = ""
	m.composeCuratorEditFile = ""
	m.reloadComposeCuration()
}

func (m *Model) cancelComposeCatalogEditor() {
	m.composeCuratorMode = composeCuratorList
	m.composeCuratorEditEntryID = ""
	m.composeCuratorEditFile = ""
	m.composeCuratorMessage = "edit cancelled"
}

func (m Model) deployComposeCatalogEntry(entry catalog.Entry, target string) error {
	return m.finishComposeDeploy(entry, target)
}

func (m Model) prepareComposeDeploy(entry catalog.Entry, target string) (Model, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return m, errors.New("deploy path is required")
	}
	files, err := catalog.ReadFiles(m.catalogDir, entry.ID)
	if err != nil {
		return m, err
	}
	system := m.activeSystemConfig()
	conflicts, err := existingComposeTargets(context.Background(), system, target, files)
	if err != nil {
		return m, err
	}
	if len(conflicts) > 0 {
		m.composeCuratorMode = composeCuratorConflict
		m.composeDeployConflicts = conflicts
		m.composeDeployPendingEntry = entry
		m.composeDeployPendingPath = target
		m.composeCuratorErr = ""
		return m, nil
	}
	if err := m.finishComposeDeploy(entry, target); err != nil {
		return m, err
	}
	m.composeCuratorMode = composeCuratorList
	m.composeCuratorMessage = "made live " + entry.Name
	m.composeCuratorErr = ""
	m.reloadComposeCuration()
	return m, nil
}

func (m Model) finishComposeDeploy(entry catalog.Entry, target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return errors.New("deploy path is required")
	}
	files, err := catalog.ReadFiles(m.catalogDir, entry.ID)
	if err != nil {
		return err
	}
	system := m.activeSystemConfig()
	written, err := writeComposeCatalogFiles(context.Background(), system, target, files)
	if err != nil {
		return err
	}
	if len(written) == 0 {
		return errors.New("no compose files were written")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := composeFilesCommand(ctx, system, entry.Name, written, "up", "-d"); err != nil {
		return err
	}
	return catalog.SetStatus(m.catalogDir, entry.ID, catalog.StatusApplied)
}

func existingComposeTargets(ctx context.Context, system config.System, target string, files []catalog.FileContent) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	targets := deployTargets(system, target, files)
	conflicts := make([]string, 0, len(targets))
	for _, target := range targets {
		exists, err := composeTargetExists(ctx, system, target)
		if err != nil {
			return nil, err
		}
		if exists {
			conflicts = append(conflicts, target)
		}
	}
	return conflicts, nil
}

func composeTargetExists(ctx context.Context, system config.System, target string) (bool, error) {
	if system.Kind == "ssh" {
		_, err := sshRun(ctx, system, "test -e "+systems.ShellQuote(target), "")
		return err == nil, nil
	}
	if _, err := os.Stat(target); err == nil {
		return true, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else {
		return false, err
	}
}

func writeComposeCatalogFiles(ctx context.Context, system config.System, target string, files []catalog.FileContent) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	targets := deployTargets(system, target, files)
	written := make([]string, 0, len(files))
	for i, file := range files {
		if system.Kind == "ssh" {
			dir := path.Dir(targets[i])
			if _, err := sshRun(ctx, system, "mkdir -p "+systems.ShellQuote(dir), ""); err != nil {
				return nil, err
			}
			if _, err := sshRun(ctx, system, "cat > "+systems.ShellQuote(targets[i]), file.Content); err != nil {
				return nil, err
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(targets[i]), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(targets[i], []byte(file.Content), 0o600); err != nil {
				return nil, err
			}
		}
		written = append(written, targets[i])
	}
	return written, nil
}

func deployTargets(system config.System, target string, files []catalog.FileContent) []string {
	singleFile := len(files) == 1 && isComposeFileCandidate(path.Base(target))
	if system.Kind != "ssh" {
		singleFile = len(files) == 1 && isComposeFileCandidate(filepath.Base(target))
	}
	out := make([]string, 0, len(files))
	for _, file := range files {
		name := file.Name
		if strings.TrimSpace(name) == "" {
			name = "compose.yml"
		}
		if singleFile {
			out = append(out, target)
			continue
		}
		if system.Kind == "ssh" {
			out = append(out, path.Join(target, path.Base(name)))
		} else {
			out = append(out, filepath.Join(target, filepath.Base(name)))
		}
	}
	return out
}

func runDockerComposeFiles(ctx context.Context, system config.System, project string, files []string, args ...string) error {
	baseArgs := []string{"compose"}
	if strings.TrimSpace(project) != "" {
		baseArgs = append(baseArgs, "-p", project)
	}
	for _, file := range files {
		baseArgs = append(baseArgs, "-f", file)
	}
	baseArgs = append(baseArgs, args...)
	if system.Kind == "ssh" {
		quoted := make([]string, len(baseArgs))
		for i, arg := range baseArgs {
			quoted[i] = systems.ShellQuote(arg)
		}
		_, err := sshRun(ctx, system, "docker "+strings.Join(quoted, " "), "")
		return err
	}
	cmd := exec.CommandContext(ctx, "docker", baseArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text == "" {
			text = err.Error()
		}
		return errors.New(text)
	}
	return nil
}

func defaultComposeDeployPath(entry catalog.Entry, system config.System) string {
	base := "compose.yml"
	if entry.PrimaryFile != "" {
		base = entry.PrimaryFile
	} else if len(entry.Files) > 0 {
		base = entry.Files[0].Name
	}
	if len(entry.Files) > 1 {
		base = ""
	}
	dir := filepath.Join(os.TempDir(), "whatthedock-compose", safeComposeFilename(entry.Name))
	if system.Kind == "ssh" {
		dir = path.Join("/tmp", "whatthedock-compose", safeComposeFilename(entry.Name))
		if base == "" {
			return dir
		}
		return path.Join(dir, base)
	}
	if base == "" {
		return dir
	}
	return filepath.Join(dir, base)
}

func firstNonEmpty(values []string, fallback string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return fallback
}

func archiveMessage(entry catalog.Entry) string {
	if entry.Archived {
		return "unarchived " + entry.Name
	}
	return "archived " + entry.Name
}

func (m Model) composeCurationOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(112, max(64, m.width-8))
	contentWidth := width - 4
	var lines []string
	lines = append(lines, m.composeCuratorCommandStrip(renderer, contentWidth))
	if m.composeCuratorMode == composeCuratorEditFile {
		editor := m.composeCuratorEditor
		editor.SetSize(contentWidth, max(8, min(24, m.height-12)))
		lines = append(lines, "", "Editing "+m.composeCuratorEditFile, editor.View(), "", renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "ctrl+s", Label: "save"},
			tideui.SoftHint{Key: "esc", Label: "cancel"},
		))
	} else if m.composeCuratorMode == composeCuratorAddSource {
		label := "URL or path"
		lines = append(lines, "", label+": "+textWithCaret(m.composeCuratorEdit, m.composeCuratorEditCursor, contentWidth-lipgloss.Width(label)-2))
		lines = append(lines, "", renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "enter", Label: "add draft"},
			tideui.SoftHint{Key: "esc", Label: "cancel"},
		))
	} else if m.composeCuratorMode == composeCuratorBrowse {
		lines = append(lines, m.composeCatalogBrowserLines(renderer, contentWidth)...)
	} else if m.composeCuratorMode == composeCuratorPreview {
		lines = append(lines, m.composeCatalogPreviewLines(renderer, contentWidth)...)
	} else if m.composeCuratorMode == composeCuratorNote || m.composeCuratorMode == composeCuratorTags || m.composeCuratorMode == composeCuratorDeploy {
		label := "Note"
		if m.composeCuratorMode == composeCuratorDeploy {
			label = "Make live path"
		} else if m.composeCuratorMode == composeCuratorTags {
			label = "Tags"
		}
		lines = append(lines, "", label+": "+textWithCaret(m.composeCuratorEdit, m.composeCuratorEditCursor, contentWidth-lipgloss.Width(label)-2))
		lines = append(lines, "", renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "enter", Label: "save"},
			tideui.SoftHint{Key: "esc", Label: "cancel"},
		))
	} else if m.composeCuratorMode == composeCuratorConflict {
		lines = append(lines, "", "Make Live will overwrite existing file(s):")
		for _, target := range m.composeDeployConflicts {
			lines = append(lines, "  "+ansi.Truncate(target, max(1, contentWidth-2), "…"))
		}
		lines = append(lines, "", renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "y", Label: "overwrite and make live"},
			tideui.SoftHint{Key: "esc", Label: "cancel"},
		))
	} else if m.composeCuratorMode == composeCuratorDelete {
		entry, _ := m.currentComposeCatalogEntry()
		lines = append(lines, "", "Delete catalog entry "+entry.Name+"?", "", renderer.RenderSoftHints(contentWidth,
			tideui.SoftHint{Key: "y", Label: "delete catalog entry"},
			tideui.SoftHint{Key: "esc", Label: "cancel"},
		))
	} else {
		lines = append(lines, "", m.composeCuratorHeader(contentWidth))
		rows := m.composeCuratorRows()
		if len(rows) == 0 {
			lines = append(lines, "No compose entries found.")
		}
		for i, row := range rows {
			lines = append(lines, m.composeCuratorRow(renderer, contentWidth, i, row))
		}
	}
	feedback := m.composeCuratorMessage
	if m.composeCuratorErr != "" {
		feedback = "error: " + m.composeCuratorErr
	}
	lines = append(lines, "", ansi.Truncate(feedback, max(1, contentWidth), "…"))
	content := renderer.RenderSoftBody(width, strings.Join(lines, "\n"))
	title := "compose curator · " + m.provider.Host().Name
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: title, Content: content, Width: width})
	return &overlay
}

func (m Model) composeCuratorCommandStrip(renderer tideui.Renderer, width int) string {
	tab := "RUNNING"
	text := "tab catalog   enter/e library edit   s save   n note   / type filter   r refresh   esc close"
	if m.composeCuratorTab == composeCuratorCatalog {
		tab = "CATALOG"
		text = "tab running   A add   B browse   N new   enter preview   e edit   c create   S draft   M live   f " + m.composeStatusLabel()
	}
	switch m.composeCuratorMode {
	case composeCuratorNote:
		text = "EDIT NOTE   enter save   esc cancel"
	case composeCuratorTags:
		text = "EDIT TAGS   comma-separated   enter save   esc cancel"
	case composeCuratorAddSource:
		text = "ADD COMPOSE   paste URL/path   enter add draft   esc cancel"
	case composeCuratorBrowse:
		text = "BROWSE COMPOSE   enter select   h/backspace up   esc back"
	case composeCuratorPreview:
		text = "PREVIEW   e edit   c create   S draft   M live   esc back"
	case composeCuratorDeploy:
		text = "MAKE LIVE   enter continue   esc cancel"
	case composeCuratorConflict:
		text = "MAKE LIVE CONFLICTS   y/enter overwrite   n/esc cancel"
	case composeCuratorEditFile:
		text = "EDIT COMPOSE YAML   ctrl+s save   esc cancel"
	case composeCuratorDelete:
		text = "DELETE CATALOG ENTRY?   y/enter delete   n/esc cancel"
	}
	text = ansi.Truncate(" "+tab+" · "+text, max(1, width), "…")
	return lipgloss.NewStyle().Background(renderer.Styles.Theme.StatusBar).Foreground(renderer.Styles.Theme.Fg).Width(width).Render(text)
}

func (m Model) composeCuratorHeader(width int) string {
	if m.composeCuratorTab == composeCuratorRunning {
		return imageTableCell("STACK", 24) + "  " + imageTableCell("SERVICES", 10) + "  " + imageTableCell("FILES", width-40)
	}
	return imageTableCell("ENTRY", 24) + "  " + imageTableCell("STATE", 10) + "  " + imageTableCell("TAGS / NOTE / SOURCE", width-40)
}

func (m Model) composeCatalogPreviewLines(renderer tideui.Renderer, width int) []string {
	entry, ok := m.currentComposeCatalogEntry()
	if !ok {
		return []string{"", "No catalog entry selected."}
	}
	content, err := catalog.Read(m.catalogDir, entry.ID)
	if err != nil {
		return []string{"", "Preview unavailable: " + ansi.Truncate(err.Error(), max(1, width-21), "…")}
	}
	state := composeEntryState(entry, m.composeCatalogEntryUnused(entry))
	tags := "(none)"
	if len(entry.Tags) > 0 {
		tags = "#" + strings.Join(entry.Tags, " #")
	}
	note := strings.TrimSpace(entry.Note)
	if note == "" {
		note = "(none)"
	}
	sources := strings.Join(entry.SourcePaths, ", ")
	if strings.TrimSpace(sources) == "" {
		sources = "(none)"
	}
	primary := firstNonEmpty([]string{entry.PrimaryFile}, "compose.yml")
	metaStyle := renderer.Styles.DetailMeta.Width(width)
	lines := []string{
		"",
		metaStyle.Render("Name     " + ansi.Truncate(entry.Name, max(1, width-9), "…")),
		metaStyle.Render("State    " + state),
		metaStyle.Render("Tags     " + ansi.Truncate(tags, max(1, width-9), "…")),
		metaStyle.Render("Note     " + ansi.Truncate(note, max(1, width-9), "…")),
		metaStyle.Render("Sources  " + ansi.Truncate(sources, max(1, width-9), "…")),
		metaStyle.Render("Primary  " + ansi.Truncate(primary, max(1, width-9), "…")),
		"",
		"Preview",
	}
	previewHeight := max(4, min(12, m.height-18))
	previewStyle := lipgloss.NewStyle().Width(width).Background(composeEditorBG)
	preview := highlightComposeYAML(content)
	if len(preview) == 0 {
		preview = []string{""}
	}
	for i := 0; i < min(previewHeight, len(preview)); i++ {
		lines = append(lines, previewStyle.Render(ansi.Truncate(preview[i], max(1, width), "…")))
	}
	if len(preview) > previewHeight {
		lines = append(lines, metaStyle.Render(fmt.Sprintf("… %d more line(s)", len(preview)-previewHeight)))
	}
	lines = append(lines, "", renderer.RenderSoftHints(width,
		tideui.SoftHint{Key: "e", Label: "edit"},
		tideui.SoftHint{Key: "c", Label: "create"},
		tideui.SoftHint{Key: "M", Label: "make live"},
		tideui.SoftHint{Key: "esc", Label: "back"},
	))
	return lines
}

func (m Model) composeCatalogBrowserLines(renderer tideui.Renderer, width int) []string {
	dirLabel := "Directory  " + short(m.composeBrowseDir, max(12, width-12))
	if system := m.activeSystemConfig(); system.Kind == "ssh" {
		dirLabel = "Directory (" + system.Name + ")  " + short(m.composeBrowseDir, max(12, width-len(system.Name)-14))
	}
	lines := []string{
		"",
		renderer.Styles.DetailMeta.Width(width).Render(dirLabel),
	}
	if m.composeBrowseLoading {
		lines = append(lines, renderer.Styles.DetailMeta.Width(width).Render("Listing remote directory…"))
	} else if m.composeBrowseErr != "" {
		lines = append(lines, renderer.Styles.StatusError.Width(width).Render(m.composeBrowseErr))
	} else if len(m.composeBrowseFiles) == 0 {
		lines = append(lines, renderer.Styles.DetailMeta.Width(width).Render("No Compose files found here."))
	} else {
		limit := max(4, min(len(m.composeBrowseFiles), m.height-12))
		start := 0
		if m.composeBrowseCursor >= limit {
			start = m.composeBrowseCursor - limit + 1
		}
		end := min(len(m.composeBrowseFiles), start+limit)
		for i := start; i < end; i++ {
			entry := m.composeBrowseFiles[i]
			text := entry.Name
			prefix := "  "
			suffix := ""
			if entry.Dir {
				prefix = "▸ "
				suffix = "dir"
			}
			if entry.Parent {
				prefix = "↰ "
				suffix = "up"
			}
			lines = append(lines, renderer.RenderSoftRow(tideui.SoftRow{
				Prefix:   prefix,
				Text:     text,
				Suffix:   suffix,
				Selected: i == m.composeBrowseCursor,
			}, width))
		}
	}
	lines = append(lines, "", renderer.RenderSoftHints(width,
		tideui.SoftHint{Key: "enter/l", Label: "open/select"},
		tideui.SoftHint{Key: "h/backspace", Label: "up"},
		tideui.SoftHint{Key: "esc", Label: "back"},
	))
	return lines
}

func (m Model) composeCuratorRow(renderer tideui.Renderer, width, index int, row any) string {
	marker := " "
	if index == m.composeCuratorCursor {
		marker = ">"
	}
	rowBG := renderer.Styles.Theme.Bg
	if index == m.composeCuratorCursor {
		if color, ok := renderer.Styles.ItemSelected.GetBackground().(lipgloss.Color); ok {
			rowBG = color
		}
	}
	base := lipgloss.NewStyle().Background(rowBG).Foreground(styleForeground(renderer.Styles.DetailBody, renderer.Styles.Theme.Fg))
	switch value := row.(type) {
	case composeStackEntry:
		files := strings.Join(value.SourcePaths, ", ")
		line := imageTableCell(marker, 1) + " " + imageTableCell(value.Project, 24) + "  " + imageTableCell(fmt.Sprintf("%d", len(value.Services)), 10) + "  " + imageTableCell(files, width-40)
		return base.Width(width).Render(line)
	case catalog.Entry:
		state := composeEntryState(value, m.composeCatalogEntryUnused(value))
		detail := composeEntryDetail(value)
		if strings.TrimSpace(detail) == "" {
			detail = strings.Join(value.SourcePaths, ", ")
		}
		line := imageTableCell(marker, 1) + " " + imageTableCell(value.Name, 24) + "  " + imageTableCell(state, 10) + "  " + imageTableCell(detail, width-40)
		return base.Width(width).Render(line)
	default:
		return ""
	}
}

func composeEntryDetail(entry catalog.Entry) string {
	parts := make([]string, 0, 2)
	if len(entry.Tags) > 0 {
		tags := make([]string, 0, len(entry.Tags))
		for _, tag := range entry.Tags {
			tags = append(tags, "#"+tag)
		}
		parts = append(parts, strings.Join(tags, " "))
	}
	if note := strings.TrimSpace(entry.Note); note != "" {
		parts = append(parts, note)
	}
	return strings.Join(parts, " · ")
}

func (m Model) composeCatalogEntryMatchesStatus(entry catalog.Entry) bool {
	unused := m.composeCatalogEntryUnused(entry)
	switch m.composeCuratorStatus {
	case composeStatusDraft:
		return !entry.Archived && entry.Status == catalog.StatusDraft
	case composeStatusSaved:
		return !entry.Archived && entry.Status == catalog.StatusSaved
	case composeStatusApplied:
		return !entry.Archived && entry.Status == catalog.StatusApplied
	case composeStatusActive:
		return !entry.Archived && !unused
	case composeStatusUnused:
		return !entry.Archived && unused
	case composeStatusArchived:
		return entry.Archived
	default:
		return true
	}
}

func (m Model) composeStatusLabel() string {
	switch m.composeCuratorStatus {
	case composeStatusDraft:
		return "draft"
	case composeStatusSaved:
		return "saved"
	case composeStatusApplied:
		return "applied"
	case composeStatusActive:
		return "active"
	case composeStatusUnused:
		return "unused"
	case composeStatusArchived:
		return "archived"
	default:
		return "all"
	}
}

func composeEntryState(entry catalog.Entry, unused bool) string {
	if entry.Archived {
		return "archived"
	}
	if unused {
		return "unused"
	}
	switch entry.Status {
	case catalog.StatusDraft:
		return "draft"
	case catalog.StatusApplied:
		return "applied"
	default:
		return "saved"
	}
}

func (m Model) composeCatalogEntryUnused(entry catalog.Entry) bool {
	for _, running := range m.composeRunning {
		if _, ok := matchingCatalogEntry([]catalog.Entry{entry}, running.SourcePaths); ok {
			return false
		}
	}
	return true
}

func textWithCaret(value string, cursor, width int) string {
	runes := []rune(value)
	cursor = clamp(cursor, 0, len(runes))
	withCaret := string(runes[:cursor]) + "|" + string(runes[cursor:])
	return imageTableCell(withCaret, max(1, width))
}
