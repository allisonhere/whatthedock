package ui

import (
	"fmt"
	"strings"

	"github.com/allisonhere/tideui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type helpEntry struct {
	Key  string
	Text string
}

type helpSection struct {
	Title   string
	Entries []helpEntry
}

// helpSections contains the commands shown in each Help category.
var helpSections = []helpSection{
	{
		Title: "Main",
		Entries: []helpEntry{
			{"Tab / Shift+Tab", "move focus between tree, activity, inspector"},
			{"Left / Right", "move focus between panes"},
			{"j / k or Up / Down", "move selection or scroll focused pane"},
			{"Enter", "select/open focused row"},
			{"Space", "expand/collapse stack row; pause/resume logs in Logs"},
			{"/", "filter stacks and containers, or filter the focused Logs pane"},
			{"r", "refresh Docker state"},
			{"Alt+r", "restart selected container"},
			{"Alt+p", "pause or unpause selected container"},
			{"s", "start/stop selected container"},
			{"n", "create container or Compose service"},
			{"m", "edit selected service; on stack row, edit whole stack"},
			{"C", "clone selected container or Compose service under a new name"},
			{"y", "Container Clipboard: yank selected container's configuration"},
			{"P", "Container Clipboard: paste the yanked container onto this host"},
			{"D", "delete selected container/service; stack row deletes stack + file"},
			{"u", "replicate: pull latest image and recreate in place"},
			{"c", "copy selected detail; inspector focus copies current row; Logs focus copies visible log lines"},
			{"o", "open selected port, mount, or compose path"},
			{"e", "open shell in selected running container"},
			{"q / Ctrl+C", "quit"},
		},
	},
	{
		Title: "Activity",
		Entries: []helpEntry{
			{"l", "show logs for the selected container or merge logs from the selected stack"},
			{"L", "expand logs across the width"},
			{"e / w / i / a", "filter logs to errors, warnings, info, or all"},
			{"f / End", "jump logs to end and follow live output"},
			{"Home / PgUp / PgDn", "move through logs"},
			{"c", "copy all currently visible (filtered) log lines"},
			{"Esc", "clear the active log filter"},
			{"p", "show problems"},
			{"a", "analyze selected problem with AI while Problems is focused"},
			{"g", "show stats graphs"},
		},
	},
	{
		Title: "Create And Edit",
		Entries: []helpEntry{
			{"[ / ]", "cycle standalone, Compose service, and Compose stack modes"},
			{"Up / Down / Tab", "move between fields"},
			{"h / l or Left / Right", "change choices or move the text cursor"},
			{"Enter", "advance field; on Compose file field, browse"},
			{"Ctrl+O", "browse for a Compose file"},
			{"Ctrl+Y", "edit Compose YAML in the embedded editor"},
			{"Ctrl+P", "open the create/edit Compose catalog picker"},
			{"Ctrl+S", "validate or save form/editor changes"},
			{"Ctrl+Enter / Alt+Enter", "confirm create/apply/deploy"},
			{"Esc / q", "close or cancel"},
		},
	},
	{
		Title: "Compose Catalog Curator",
		Entries: []helpEntry{
			{"Ctrl+K", "open command palette, then run Curate Compose files"},
			{"Tab", "switch library, live, and unused views"},
			{"j / k", "move catalog selection"},
			{"f", "filter catalog entries"},
			{"A", "add draft from URL or path"},
			{"B", "browse for a Compose file and add it as a draft"},
			{"N", "create a blank draft"},
			{"Enter / l", "preview catalog entry"},
			{"e", "edit catalog entry in the Compose editor"},
			{"c", "create a runnable draft from a live stack"},
			{"S", "save a live stack as a draft"},
			{"M / p", "make catalog entry live"},
			{"s", "status: draft, live, or archived"},
			{"m", "toggle missing/unused review"},
			{"n / t", "edit note or tags; notes are kept as Compose comments"},
			{"a", "archive or unarchive"},
			{"D", "delete catalog entry"},
		},
	},
	{
		Title: "Curators",
		Entries: []helpEntry{
			{"Ctrl+K", "open image, network, volume, or Compose curators"},
			{"j / k", "move through images, networks, or volumes"},
			{"Space", "select removable image, network, or volume"},
			{"r", "reload curator data"},
			{"d", "delete selected removable items"},
			{"y / Enter", "confirm curator deletion"},
			{"n / Esc", "cancel curator deletion"},
		},
	},
	{
		// Container Clipboard: yank a container's configuration on one
		// Docker host, switch to another configured system, paste it there.
		// v1 clones configuration only — never volume/bind-mount data, and
		// never touches the source container.
		Title: "Container Clipboard",
		Entries: []helpEntry{
			{"y", "yank the selected container's configuration"},
			{"P", "paste the yanked container onto the current host"},
			{"Enter", "on the paste review screen: open it for editing before deploying"},
			{"d", "on the paste review screen: deploy without further edits"},
			{"t", "on the paste review screen: redirect missing bind-mount paths to a placeholder"},
			{"Esc", "on the paste review screen: cancel (the clipboard item is kept)"},
		},
	},
	{
		Title: "Overlays",
		Entries: []helpEntry{
			{"Ctrl+K", "command palette. Type to search, use arrows to browse, and press Enter to run."},
			{"?", "keyboard help"},
			{"/", "search inside Help while Help is open"},
			{"d", "dashboard: fleet summary and container stats"},
			{"Dashboard Enter", "open highlighted container"},
			{"Dashboard s", "cycle sort: name, CPU, memory, network"},
			{"Dashboard g", "group the list by Compose stack"},
			{"Dashboard p", "jump from dashboard to Problems"},
			{"T", "theme picker"},
			{", / Ctrl+,", "settings"},
			{"S", "systems"},
			{"A", "about screen"},
			{"Esc / q", "close the current overlay"},
		},
	},
	{
		Title: "Settings And Systems",
		Entries: []helpEntry{
			{"Settings j / k", "switch section (sidebar) or move between rows; Esc steps back out"},
			{"Settings Enter / Space", "edit text rows, trigger actions, or change choices"},
			{"Settings h / l", "change choice rows"},
			{"Settings Ctrl+S", "save settings"},
			{"Systems Enter", "switch to selected Docker system"},
			{"Systems t", "test selected system"},
			{"Systems a / e", "add or edit a system"},
			{"Systems d", "delete inactive system"},
			{"System edit Ctrl+S", "save system"},
			{"Ctrl+K", "shut down or reboot the current host (stops containers first, not available in demo mode)"},
		},
	},
}

const helpStatusTitle = "Status Colors"
const helpPaneDivider = " │ "

func filterHelpSections(query string) []helpSection {
	query = strings.ToLower(strings.TrimSpace(query))
	status := helpSection{Title: helpStatusTitle}
	for _, entry := range statusLegendEntries {
		status.Entries = append(status.Entries, helpEntry{entry.glyph, entry.label})
	}
	categories := make([]helpSection, 0, len(helpSections)+1)
	for _, section := range helpSections {
		categories = appendHelpMatches(categories, section, query)
	}
	return appendHelpMatches(categories, status, query)
}

func appendHelpMatches(categories []helpSection, section helpSection, query string) []helpSection {
	if strings.Contains(strings.ToLower(section.Title), query) {
		return append(categories, section)
	}
	matches := helpSection{Title: section.Title}
	for _, entry := range section.Entries {
		if strings.Contains(strings.ToLower(entry.Key+" "+entry.Text), query) {
			matches.Entries = append(matches.Entries, entry)
		}
	}
	if len(matches.Entries) == 0 {
		return categories
	}
	return append(categories, matches)
}

type helpLayout struct {
	width, left, right, rows int
	footer                   bool
}

func (m Model) helpLayout() helpLayout {
	width := max(6, min(102, m.width-4))
	footer := m.height >= 7
	rows := min(33, m.height-5)
	if footer {
		rows--
	}
	inner := width - 4
	layout := helpLayout{width: width, right: inner, rows: max(1, rows), footer: footer}
	if width >= 64 {
		layout.left = min(24, inner/3) - 2
		layout.right = inner - layout.left - ansi.StringWidth(helpPaneDivider)
	}
	return layout
}

func helpDetailLines(section helpSection, width int) []string {
	lines := []string{section.Title}
	keyWidth := min(23, max(1, min(width-1, max(8, width/3))))
	for _, entry := range section.Entries {
		// Keep a long shortcut intact on narrow screens by putting its
		// description below it when it doesn't fit in the key column.
		if ansi.StringWidth(entry.Key) >= keyWidth {
			lines = append(lines, strings.Split(ansi.Hardwrap(entry.Key, width, false), "\n")...)
			for _, line := range wrapHelpText(entry.Text, max(1, width-2)) {
				lines = append(lines, "  "+line)
			}
			continue
		}
		for i, line := range wrapHelpText(entry.Text, max(1, width-keyWidth)) {
			key := ""
			if i == 0 {
				key = entry.Key
			}
			lines = append(lines, key+strings.Repeat(" ", keyWidth-ansi.StringWidth(key))+line)
		}
	}
	return lines
}

func wrapHelpText(text string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && ansi.StringWidth(line)+1+ansi.StringWidth(word) > width {
			lines = append(lines, line)
			line = ""
		}
		if ansi.StringWidth(word) > width {
			parts := strings.Split(ansi.Hardwrap(word, width, false), "\n")
			lines = append(lines, parts[:len(parts)-1]...)
			line = parts[len(parts)-1]
		} else if line == "" {
			line = word
		} else {
			line += " " + word
		}
	}
	return append(lines, line)
}

func (m *Model) openHelp() {
	m.overlay = overlayHelp
	m.helpRight = false
	m.resetHelpSearch("")
	m.helpSearchActive = false
}

func (m *Model) resetHelpSearch(query string) {
	m.helpSearch = query
	m.helpCategory, m.helpScroll = 0, 0
}

func (m *Model) handleHelpKey(msg tea.KeyMsg) {
	if m.helpSearchActive {
		switch msg.String() {
		case "esc":
			m.helpSearchActive = false
			m.resetHelpSearch("")
		case "enter":
			m.helpSearchActive = false
		case "backspace":
			m.resetHelpSearch(dropLastRune(m.helpSearch))
		case "ctrl+u":
			m.resetHelpSearch("")
		default:
			if len(msg.Runes) > 0 {
				m.resetHelpSearch(m.helpSearch + string(msg.Runes))
			}
		}
		return
	}
	switch msg.String() {
	case "/":
		m.helpSearchActive = true
	case "esc":
		if m.helpSearch != "" {
			m.resetHelpSearch("")
			return
		}
		m.overlay = overlayNone
	case "q", "?":
		m.overlay = overlayNone
	case "left", "h", "shift+tab":
		m.helpRight = false
	case "right", "l", "tab", "enter":
		if len(filterHelpSections(m.helpSearch)) > 0 {
			m.helpRight = true
		}
	case "j", "down":
		m.moveHelp(1)
	case "k", "up":
		m.moveHelp(-1)
	case "pgdown":
		m.moveHelp(max(1, m.helpLayout().rows-1))
	case "pgup":
		m.moveHelp(-max(1, m.helpLayout().rows-1))
	case "g", "home":
		m.moveHelp(-m.helpNavigationSize())
	case "G", "end":
		m.moveHelp(m.helpNavigationSize())
	}
}

func (m Model) helpNavigationSize() int {
	if m.helpRight {
		return m.helpDetailSize()
	}
	return len(filterHelpSections(m.helpSearch))
}

func (m Model) helpDetailSize() int {
	sections := filterHelpSections(m.helpSearch)
	if len(sections) == 0 {
		return 0
	}
	section := sections[clamp(m.helpCategory, 0, len(sections)-1)]
	width := m.helpLayout().right
	if helpShowsStatusLegend(section, width) {
		return statusLegendLineCount
	}
	return len(helpDetailLines(section, width))
}

func (m *Model) moveHelp(delta int) {
	if m.helpRight {
		limit := max(0, m.helpDetailSize()-m.helpLayout().rows)
		m.helpScroll = clamp(clamp(m.helpScroll, 0, limit)+delta, 0, limit)
	} else {
		m.helpCategory = clamp(m.helpCategory+delta, 0, max(0, m.helpNavigationSize()-1))
		m.helpScroll = 0
	}
}

func helpShowsStatusLegend(section helpSection, width int) bool {
	return section.Title == helpStatusTitle && len(section.Entries) == len(statusLegendEntries) && width >= 28
}

func (m Model) helpOverlay(renderer tideui.Renderer) *tideui.Overlay {
	layout := m.helpLayout()
	sections := filterHelpSections(m.helpSearch)
	style := renderer.Styles.OverlayBody
	inner := layout.width - 4
	category := clamp(m.helpCategory, 0, max(0, len(sections)-1))
	lines := make([]string, 0, layout.rows+1)
	switch {
	case len(sections) == 0:
		lines = append(lines, padPlainLine(style, fmt.Sprintf("No help entries match %q", m.helpSearch), inner))
	case layout.left == 0 && !m.helpRight:
		lines = m.helpCategoryRows(renderer, sections, category, inner, layout.rows)
	default:
		detail := helpDetailLines(sections[category], layout.right)
		if helpShowsStatusLegend(sections[category], layout.right) {
			detail = statusLegendLines(renderer)
		}
		start := clamp(m.helpScroll, 0, max(0, len(detail)-layout.rows))
		detail = detail[start:min(start+layout.rows, len(detail))]
		if layout.left == 0 {
			for _, line := range detail {
				lines = append(lines, padPlainLine(style, line, layout.right))
			}
		} else {
			left := m.helpCategoryRows(renderer, sections, category, layout.left, layout.rows)
			divider := style.Render(helpPaneDivider)
			if m.helpRight {
				divider = style.Copy().Foreground(renderer.Styles.Theme.BorderFocus).Render(" ┃ ")
			}
			for i, row := range left {
				text := ""
				if i < len(detail) {
					text = detail[i]
				}
				lines = append(lines, row+divider+padPlainLine(style, text, layout.right))
			}
		}
	}
	for len(lines) < layout.rows {
		lines = append(lines, padPlainLine(style, "", inner))
	}
	if layout.footer {
		lines = append(lines, m.helpFooter(renderer, inner))
	}
	content := renderer.RenderSoftBody(layout.width, strings.Join(lines, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "help", Content: content, Width: layout.width})
	return &overlay
}

func (m Model) helpCategoryRows(renderer tideui.Renderer, sections []helpSection, category, width, rows int) []string {
	start, end := visibleRange(len(sections), category, rows)
	lines := make([]string, 0, rows)
	for i := start; i < end; i++ {
		lines = append(lines, renderer.RenderSoftRow(tideui.SoftRow{Text: sections[i].Title, Selected: i == category}, width))
	}
	for len(lines) < rows {
		lines = append(lines, padPlainLine(renderer.Styles.OverlayBody, "", width))
	}
	return lines
}

func (m Model) helpFooter(renderer tideui.Renderer, width int) string {
	style := renderer.Styles.OverlayBody
	if m.helpSearchActive {
		input := m.helpSearch + "│"
		if overflow := ansi.StringWidth(input) - max(1, width-2); overflow > 0 {
			input = ansi.Cut(input, overflow, ansi.StringWidth(input))
		}
		return padPlainLine(style.Copy().Bold(true), "/ "+input, width)
	}
	if m.helpSearch != "" {
		return padPlainLine(style, "/ edit  esc clear  ←→ panes  ↑↓ move", width)
	}
	if m.helpLayout().left == 0 {
		if m.helpRight {
			return padPlainLine(style, "← categories  ↑↓ scroll  / search", width)
		}
		return padPlainLine(style, "enter open  / search  esc close", width)
	}
	return padPlainLine(style, "/ search  ←→ pane  ↑↓ choose/scroll  esc close", width)
}

func (m *Model) handleHelpMouse(msg tea.MouseMsg) {
	if m.helpSearchActive {
		return
	}
	layout := m.helpLayout()
	sections := filterHelpSections(m.helpSearch)
	if len(sections) == 0 {
		return
	}
	boxWidth, boxHeight := layout.width+2, layout.rows+4
	if layout.footer {
		boxHeight++
	}
	x := msg.X - max(0, (m.width-boxWidth)/2) - 3
	y := msg.Y - 1 - max(0, (m.height-1-boxHeight)/2) - 2
	if x < 0 || x >= layout.width-4 || y < 0 || y >= layout.rows {
		return
	}
	categoryPane := !m.helpRight
	if layout.left > 0 {
		if x >= layout.left && x < layout.left+ansi.StringWidth(helpPaneDivider) {
			return
		}
		categoryPane = x < layout.left
	}
	switch msg.Button {
	case tea.MouseButtonLeft:
		if categoryPane {
			start, end := visibleRange(len(sections), m.helpCategory, layout.rows)
			if i := start + y; i < end {
				m.helpCategory, m.helpScroll = i, 0
				m.helpRight = layout.left == 0
			}
		} else {
			m.helpRight = true
		}
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		delta := 1
		if msg.Button == tea.MouseButtonWheelUp {
			delta = -1
		}
		if categoryPane {
			m.helpCategory = clamp(m.helpCategory+delta, 0, len(sections)-1)
			m.helpScroll = 0
		} else {
			limit := max(0, m.helpDetailSize()-layout.rows)
			m.helpScroll = clamp(clamp(m.helpScroll, 0, limit)+3*delta, 0, limit)
		}
	}
}
