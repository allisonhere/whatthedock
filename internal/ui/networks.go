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

func (m Model) openNetworkCuration() (tea.Model, tea.Cmd) {
	m.overlay = overlayNetworkCuration
	m.networkCursor = 0
	m.networkSelected = map[string]bool{}
	m.networkLoading = true
	m.networkConfirming = false
	m.networkRemoving = false
	m.networkError = ""
	m.networkResult = ""
	return m, m.loadNetworksCmd()
}

func (m Model) loadNetworksCmd() tea.Cmd {
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		networks, err := provider.Networks(ctx)
		return networkListMsg{networks: networks, err: err}
	}
}

func (m Model) selectedNetworkCount() int {
	count := 0
	for _, network := range m.networkItems {
		if network.Removable() && m.networkSelected[network.ID] {
			count++
		}
	}
	return count
}

func (m Model) removableNetworks() []domain.Network {
	networks := make([]domain.Network, 0, len(m.networkItems))
	for _, network := range m.networkItems {
		if network.Removable() {
			networks = append(networks, network)
		}
	}
	return networks
}

func (m Model) selectedNetworkRefs() []string {
	refs := make([]string, 0, m.selectedNetworkCount())
	for _, network := range m.networkItems {
		if network.Removable() && m.networkSelected[network.ID] {
			refs = append(refs, network.ID)
		}
	}
	return refs
}

func (m Model) removeNetworksCmd(refs []string) tea.Cmd {
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		result := networkRemoveDoneMsg{}
		for _, ref := range refs {
			if err := provider.RemoveNetwork(ctx, ref); err != nil {
				result.failures = append(result.failures, shortNetworkRef(ref)+": "+friendlyDockerError(err))
				continue
			}
			result.removed = append(result.removed, ref)
		}
		return result
	}
}

func (m Model) handleNetworkCurationKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.networkLoading {
		if msg.String() == "esc" {
			m.overlay = overlayNone
		}
		return m, nil
	}
	if m.networkRemoving {
		return m, nil
	}
	if m.networkConfirming {
		switch msg.String() {
		case "esc", "n":
			m.networkConfirming = false
		case "y", "enter":
			refs := m.selectedNetworkRefs()
			if len(refs) == 0 {
				m.networkConfirming = false
				return m, nil
			}
			m.networkRemoving = true
			return m, m.removeNetworksCmd(refs)
		}
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.overlay = overlayNone
	case "j", "down":
		m.networkCursor = clamp(m.networkCursor+1, 0, max(0, len(m.networkItems)-1))
	case "k", "up":
		m.networkCursor = clamp(m.networkCursor-1, 0, max(0, len(m.networkItems)-1))
	case "r":
		m.networkLoading = true
		m.networkError = ""
		return m, m.loadNetworksCmd()
	case " ", "space":
		if len(m.networkItems) > 0 {
			network := m.networkItems[m.networkCursor]
			if network.Removable() {
				m.networkSelected[network.ID] = !m.networkSelected[network.ID]
				m.networkCursor = clamp(m.networkCursor+1, 0, max(0, len(m.networkItems)-1))
			}
		}
	case "d":
		if m.selectedNetworkCount() > 0 {
			m.networkConfirming = true
		}
	}
	return m, nil
}

func (m Model) networkCurationOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(106, max(58, m.width-8))
	contentWidth := width - 4
	if m.networkLoading {
		content := renderer.RenderSoftBody(width, "Loading networks…\n\n"+renderer.RenderSoftHints(contentWidth, tideui.SoftHint{Key: "esc", Label: "cancel"}))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "network curator · " + m.provider.Host().Name, Content: content, Width: width})
		return &overlay
	}
	lines := []string{m.networkCommandStrip(renderer, contentWidth)}
	if len(m.networkItems) == 0 {
		lines = append(lines, "", "No networks found on this Docker host.")
	} else {
		lines = append(lines, "", networkTableHeader(contentWidth))
		for i, network := range m.networkItems {
			marker := " "
			if i == m.networkCursor {
				marker = ">"
			}
			selected := " "
			if m.networkSelected[network.ID] {
				selected = "x"
			}
			lines = append(lines, networkTableRow(renderer, contentWidth, i, marker, i == m.networkCursor, selected, network))
		}
	}
	// Keep this summary row present even with no selection so choosing the
	// first network cannot change the modal height or move the table upward.
	lines = append(lines, "", fmt.Sprintf("selected: %d network(s)", m.selectedNetworkCount()))
	feedback := m.networkResult
	if m.networkError != "" {
		feedback = "error: " + m.networkError
	}
	// Reserve one feedback row up front. Completion and failure messages stay
	// on that row so the modal never grows when removal reports back.
	lines = append(lines, "", ansi.Truncate(feedback, max(1, contentWidth), "…"))
	content := renderer.RenderSoftBody(width, strings.Join(lines, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "network curator · " + m.provider.Host().Name, Content: content, Width: width})
	return &overlay
}

func (m Model) networkCommandStrip(renderer tideui.Renderer, width int) string {
	text := "space select   d delete   r refresh   esc close"
	fg := renderer.Styles.Theme.Fg
	bg := renderer.Styles.Theme.StatusBar
	bold := false
	switch {
	case m.networkRemoving:
		text = "REMOVING SELECTED NETWORKS..."
		fg, bg, bold = lipgloss.Color("#7dcfff"), lipgloss.Color("#24313a"), true
	case m.networkConfirming:
		text = fmt.Sprintf("DELETE %d NETWORK(S)?   y/enter continue   n/esc cancel", m.selectedNetworkCount())
		fg, bg, bold = lipgloss.Color("#ffd7d9"), lipgloss.Color("#4a2429"), true
	case m.selectedNetworkCount() > 0:
		text = fmt.Sprintf("%d NETWORK(S) SELECTED   d DELETE", m.selectedNetworkCount())
		fg, bg, bold = lipgloss.Color("#ffe3a3"), lipgloss.Color("#413724"), true
	}
	text = ansi.Truncate(" "+text, max(1, width), "…")
	return lipgloss.NewStyle().Background(bg).Foreground(fg).Bold(bold).Width(width).Render(text)
}

func shortNetworkRef(ref string) string {
	if len(ref) > 12 {
		return ref[:12]
	}
	return ref
}

func sortNetworks(networks []domain.Network) {
	sort.Slice(networks, func(i, j int) bool { return networks[i].Name < networks[j].Name })
}

func networkTableHeader(width int) string {
	nameWidth, subnetWidth, stateWidth, idWidth := networkColumnWidths(width)
	return imageTableCell("", 1) + " " + imageTableCell("", 3) + " " +
		imageTableCell("NETWORK", nameWidth) + "  " + imageTableCell("SUBNET", subnetWidth) + "  " +
		imageTableCell("STATE", stateWidth) + "  " + imageTableCell("ID", idWidth)
}

func networkTableRow(renderer tideui.Renderer, width, index int, marker string, highlighted bool, selected string, network domain.Network) string {
	nameWidth, subnetWidth, stateWidth, idWidth := networkColumnWidths(width)
	state := "USED"
	stateColor := lipgloss.Color("#80c990")
	if network.Removable() {
		state = "UNUSED"
		stateColor = lipgloss.Color("#e06c75")
	} else if network.Containers == 0 {
		state = "BUILT-IN"
		stateColor = lipgloss.Color("#7f92a8")
	}
	rowBG := stripedRowBackground(renderer, index%2 == 1, highlighted)
	base := lipgloss.NewStyle().Background(rowBG).Foreground(styleForeground(renderer.Styles.DetailBody, renderer.Styles.Theme.Fg))
	stateCell := base.Foreground(stateColor).Bold(true).Render(imageTableCell(state, stateWidth))
	row := base.Render(imageTableCell(marker, 1)) + base.Render(" ") + base.Render(imageTableCell("["+selected+"]", 3)) + base.Render(" ") +
		base.Render(imageTableCell(network.Name, nameWidth)) + base.Render("  ") +
		base.Render(imageTableCell(network.Subnet, subnetWidth)) + base.Render("  ") +
		stateCell + base.Render("  ") + base.Render(imageTableCell(network.ShortID(), idWidth))
	return lipgloss.NewStyle().Background(rowBG).Width(width).Render(row)
}

func networkColumnWidths(width int) (name, subnet, state, id int) {
	subnet, state, id = 18, 8, 12
	name = max(18, width-1-1-3-3-2-2-subnet-state-id)
	return name, subnet, state, id
}
