package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/allisonhere/tideui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/allisonhere/whatthedock/internal/domain"
)

func (m Model) openVolumeCuration() (tea.Model, tea.Cmd) {
	m.overlay = overlayVolumeCuration
	m.volumeCursor = 0
	m.volumeSelected = map[string]bool{}
	m.volumeLoading = true
	m.volumeConfirming = false
	m.volumeRemoving = false
	m.volumeError = ""
	m.volumeResult = ""
	return m, m.loadVolumesCmd()
}

func (m Model) loadVolumesCmd() tea.Cmd {
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		volumes, err := provider.Volumes(ctx)
		return volumeListMsg{volumes: volumes, err: err}
	}
}

func (m Model) selectedVolumeCount() int {
	count := 0
	for _, volume := range m.volumeItems {
		if volume.Removable() && m.volumeSelected[volume.Name] {
			count++
		}
	}
	return count
}

func (m Model) removableVolumes() []domain.Volume {
	volumes := make([]domain.Volume, 0, len(m.volumeItems))
	for _, volume := range m.volumeItems {
		if volume.Removable() {
			volumes = append(volumes, volume)
		}
	}
	return volumes
}

func (m Model) selectedVolumeRefs() []string {
	refs := make([]string, 0, m.selectedVolumeCount())
	for _, volume := range m.volumeItems {
		if volume.Removable() && m.volumeSelected[volume.Name] {
			refs = append(refs, volume.Name)
		}
	}
	return refs
}

func (m Model) removeVolumesCmd(refs []string) tea.Cmd {
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		result := volumeRemoveDoneMsg{}
		for _, ref := range refs {
			if err := provider.RemoveVolume(ctx, ref); err != nil {
				result.failures = append(result.failures, ref+": "+friendlyDockerError(err))
				continue
			}
			result.removed = append(result.removed, ref)
		}
		return result
	}
}

func (m Model) handleVolumeCurationKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.volumeLoading {
		if msg.String() == "esc" {
			m.overlay = overlayNone
		}
		return m, nil
	}
	if m.volumeRemoving {
		return m, nil
	}
	if m.volumeConfirming {
		switch msg.String() {
		case "esc", "n":
			m.volumeConfirming = false
		case "y", "enter":
			refs := m.selectedVolumeRefs()
			if len(refs) == 0 {
				m.volumeConfirming = false
				return m, nil
			}
			m.volumeRemoving = true
			return m, m.removeVolumesCmd(refs)
		}
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.overlay = overlayNone
	case "j", "down":
		m.volumeCursor = clamp(m.volumeCursor+1, 0, max(0, len(m.volumeItems)-1))
	case "k", "up":
		m.volumeCursor = clamp(m.volumeCursor-1, 0, max(0, len(m.volumeItems)-1))
	case "r":
		m.volumeLoading = true
		m.volumeError = ""
		return m, m.loadVolumesCmd()
	case " ", "space":
		if len(m.volumeItems) > 0 {
			volume := m.volumeItems[m.volumeCursor]
			if volume.Removable() {
				m.volumeSelected[volume.Name] = !m.volumeSelected[volume.Name]
				m.volumeCursor = clamp(m.volumeCursor+1, 0, max(0, len(m.volumeItems)-1))
			}
		}
	case "d":
		if m.selectedVolumeCount() > 0 {
			m.volumeConfirming = true
		}
	}
	return m, nil
}

func (m Model) volumeCurationOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(106, max(58, m.width-8))
	contentWidth := width - 4
	if m.volumeLoading {
		content := renderer.RenderSoftBody(width, "Loading volumes…\n\n"+renderer.RenderSoftHints(contentWidth, tideui.SoftHint{Key: "esc", Label: "cancel"}))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "volume curator · " + m.provider.Host().Name, Content: content, Width: width})
		return &overlay
	}
	lines := []string{m.volumeCommandStrip(renderer, contentWidth)}
	if len(m.volumeItems) == 0 {
		lines = append(lines, "", "No volumes found on this Docker host.")
	} else {
		lines = append(lines, "", volumeTableHeader(contentWidth))
		for i, volume := range m.volumeItems {
			marker := " "
			if i == m.volumeCursor {
				marker = ">"
			}
			selected := " "
			if m.volumeSelected[volume.Name] {
				selected = "x"
			}
			lines = append(lines, volumeTableRow(renderer, contentWidth, i, marker, i == m.volumeCursor, selected, volume))
		}
	}
	// Keep this summary row present even with no selection so choosing the
	// first volume cannot change the modal height or move the table upward.
	lines = append(lines, "", fmt.Sprintf("selected: %d volume(s)", m.selectedVolumeCount()))
	feedback := m.volumeResult
	if m.volumeError != "" {
		feedback = "error: " + m.volumeError
	}
	// Reserve one feedback row up front. Completion and failure messages stay
	// on that row so the modal never grows when removal reports back.
	lines = append(lines, "", ansi.Truncate(feedback, max(1, contentWidth), "…"))
	content := renderer.RenderSoftBody(width, strings.Join(lines, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "volume curator · " + m.provider.Host().Name, Content: content, Width: width})
	return &overlay
}

func (m Model) volumeCommandStrip(renderer tideui.Renderer, width int) string {
	text := "space select   d delete   r refresh   esc close"
	fg := renderer.Styles.Theme.Fg
	bg := renderer.Styles.Theme.StatusBar
	bold := false
	switch {
	case m.volumeRemoving:
		text = "REMOVING SELECTED VOLUMES..."
		fg, bg, bold = lipgloss.Color("#7dcfff"), lipgloss.Color("#24313a"), true
	case m.volumeConfirming:
		text = fmt.Sprintf("DELETE %d VOLUME(S)?   y/enter continue   n/esc cancel", m.selectedVolumeCount())
		fg, bg, bold = lipgloss.Color("#ffd7d9"), lipgloss.Color("#4a2429"), true
	case m.selectedVolumeCount() > 0:
		text = fmt.Sprintf("%d VOLUME(S) SELECTED   d DELETE", m.selectedVolumeCount())
		fg, bg, bold = lipgloss.Color("#ffe3a3"), lipgloss.Color("#413724"), true
	}
	text = ansi.Truncate(" "+text, max(1, width), "…")
	return lipgloss.NewStyle().Background(bg).Foreground(fg).Bold(bold).Width(width).Render(text)
}

func sortVolumes(volumes []domain.Volume) {
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].Name < volumes[j].Name })
}

func volumeTableHeader(width int) string {
	nameWidth, mountWidth, stateWidth := volumeColumnWidths(width)
	return imageTableCell("", 1) + " " + imageTableCell("", 3) + " " +
		imageTableCell("VOLUME", nameWidth) + "  " + imageTableCell("MOUNTPOINT", mountWidth) + "  " +
		imageTableCell("STATE", stateWidth)
}

func volumeTableRow(renderer tideui.Renderer, width, index int, marker string, highlighted bool, selected string, volume domain.Volume) string {
	nameWidth, mountWidth, stateWidth := volumeColumnWidths(width)
	state := "USED"
	stateColor := lipgloss.Color("#80c990")
	if volume.Removable() {
		state = "UNUSED"
		stateColor = lipgloss.Color("#e06c75")
	}
	rowBG := stripedRowBackground(renderer, index%2 == 1, highlighted)
	base := lipgloss.NewStyle().Background(rowBG).Foreground(styleForeground(renderer.Styles.DetailBody, renderer.Styles.Theme.Fg))
	stateCell := base.Foreground(stateColor).Bold(true).Render(imageTableCell(state, stateWidth))
	row := base.Render(imageTableCell(marker, 1)) + base.Render(" ") + base.Render(imageTableCell("["+selected+"]", 3)) + base.Render(" ") +
		base.Render(imageTableCell(volume.Name, nameWidth)) + base.Render("  ") +
		base.Render(imageTableCell(volume.Mountpoint, mountWidth)) + base.Render("  ") +
		stateCell
	return lipgloss.NewStyle().Background(rowBG).Width(width).Render(row)
}

func volumeColumnWidths(width int) (name, mount, state int) {
	state = 8
	name = max(16, (width-1-1-3-3-2-2-state)/2)
	mount = max(16, width-1-1-3-3-2-2-state-name)
	return name, mount, state
}
