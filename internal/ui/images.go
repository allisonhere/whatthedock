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

func (m Model) openImageCuration() (tea.Model, tea.Cmd) {
	m.overlay = overlayImageCuration
	m.imageCursor = 0
	m.imageSelected = map[string]bool{}
	m.imageLoading = true
	m.imageConfirming = false
	m.imageRemoving = false
	m.imageError = ""
	m.imageResult = ""
	return m, m.loadImagesCmd()
}

func (m Model) loadImagesCmd() tea.Cmd {
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		images, err := provider.Images(ctx)
		return imageListMsg{images: images, err: err}
	}
}

func (m Model) selectedImageCount() int {
	count := 0
	for _, image := range m.imageItems {
		if image.Removable() && m.imageSelected[image.ID.ID] {
			count++
		}
	}
	return count
}

func (m Model) selectedImageBytes() int64 {
	var total int64
	for _, image := range m.imageItems {
		if image.Removable() && m.imageSelected[image.ID.ID] {
			total += maxInt64(0, image.Size)
		}
	}
	return total
}

func (m Model) removableImages() []domain.Image {
	images := make([]domain.Image, 0, len(m.imageItems))
	for _, image := range m.imageItems {
		if image.Removable() {
			images = append(images, image)
		}
	}
	return images
}

func (m Model) selectedImageRefs() []string {
	refs := make([]string, 0, m.selectedImageCount())
	for _, image := range m.imageItems {
		if image.Removable() && m.imageSelected[image.ID.ID] {
			refs = append(refs, image.ID.ID)
		}
	}
	return refs
}

func (m Model) removeImagesCmd(refs []string) tea.Cmd {
	provider := m.provider
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		result := imageRemoveDoneMsg{}
		for _, ref := range refs {
			if err := provider.RemoveImage(ctx, ref); err != nil {
				result.failures = append(result.failures, shortImageRef(ref)+": "+friendlyDockerError(err))
				continue
			}
			result.removed = append(result.removed, ref)
		}
		return result
	}
}

func (m Model) handleImageCurationKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.imageLoading {
		if msg.String() == "esc" {
			m.overlay = overlayNone
		}
		return m, nil
	}
	if m.imageRemoving {
		return m, nil
	}
	if m.imageConfirming {
		switch msg.String() {
		case "esc", "n":
			m.imageConfirming = false
		case "y", "enter":
			refs := m.selectedImageRefs()
			if len(refs) == 0 {
				m.imageConfirming = false
				return m, nil
			}
			m.imageRemoving = true
			return m, m.removeImagesCmd(refs)
		}
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.overlay = overlayNone
	case "j", "down":
		m.imageCursor = clamp(m.imageCursor+1, 0, max(0, len(m.imageItems)-1))
	case "k", "up":
		m.imageCursor = clamp(m.imageCursor-1, 0, max(0, len(m.imageItems)-1))
	case "r":
		m.imageLoading = true
		m.imageError = ""
		return m, m.loadImagesCmd()
	case " ", "space":
		if len(m.imageItems) > 0 {
			image := m.imageItems[m.imageCursor]
			if image.Removable() {
				m.imageSelected[image.ID.ID] = !m.imageSelected[image.ID.ID]
				m.imageCursor = clamp(m.imageCursor+1, 0, max(0, len(m.imageItems)-1))
			}
		}
	case "d":
		if m.selectedImageCount() > 0 {
			m.imageConfirming = true
		}
	}
	return m, nil
}

func (m Model) imageCurationOverlay(renderer tideui.Renderer) *tideui.Overlay {
	width := min(106, max(58, m.width-8))
	contentWidth := width - 4
	if m.imageLoading {
		content := renderer.RenderSoftBody(width, "Loading images…\n\n"+renderer.RenderSoftHints(contentWidth, tideui.SoftHint{Key: "esc", Label: "cancel"}))
		overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "image curator · " + m.provider.Host().Name, Content: content, Width: width})
		return &overlay
	}
	lines := []string{m.imageCommandStrip(renderer, contentWidth)}
	if len(m.imageItems) == 0 {
		lines = append(lines, "", "No images found on this Docker host.")
	} else {
		lines = append(lines, "", imageTableHeader(contentWidth))
		for i, image := range m.imageItems {
			marker := " "
			if i == m.imageCursor {
				marker = ">"
			}
			selected := " "
			if m.imageSelected[image.ID.ID] {
				selected = "x"
			}
			lines = append(lines, imageTableRow(renderer, contentWidth, marker, i == m.imageCursor, selected, image))
		}
	}
	// Keep this summary row present even with no selection so choosing the
	// first image cannot change the modal height or move the table upward.
	lines = append(lines, "", fmt.Sprintf("selected: %d image(s), %s reclaimable", m.selectedImageCount(), formatBytes(uint64(m.selectedImageBytes()))))
	feedback := m.imageResult
	if m.imageError != "" {
		feedback = "error: " + m.imageError
	}
	// Reserve one feedback row up front. Completion and failure messages stay
	// on that row so the modal never grows when deletion reports back.
	lines = append(lines, "", ansi.Truncate(feedback, max(1, contentWidth), "…"))
	content := renderer.RenderSoftBody(width, strings.Join(lines, "\n"))
	overlay := renderer.SoftPanelOverlay(tideui.SoftPanel{Prefix: "whatthedock", Title: "image curator · " + m.provider.Host().Name, Content: content, Width: width})
	return &overlay
}

func (m Model) imageCommandStrip(renderer tideui.Renderer, width int) string {
	text := "space select   d delete   r refresh   esc close"
	fg := renderer.Styles.Theme.Fg
	bg := renderer.Styles.Theme.StatusBar
	bold := false
	switch {
	case m.imageRemoving:
		text = "REMOVING SELECTED IMAGES..."
		fg, bg, bold = lipgloss.Color("#7dcfff"), lipgloss.Color("#24313a"), true
	case m.imageConfirming:
		text = fmt.Sprintf("DELETE %d IMAGE(S)?   y/enter continue   n/esc cancel", m.selectedImageCount())
		fg, bg, bold = lipgloss.Color("#ffd7d9"), lipgloss.Color("#4a2429"), true
	case m.selectedImageCount() > 0:
		text = fmt.Sprintf("%d IMAGE(S) SELECTED · %s RECLAIMABLE   d DELETE", m.selectedImageCount(), formatBytes(uint64(m.selectedImageBytes())))
		fg, bg, bold = lipgloss.Color("#ffe3a3"), lipgloss.Color("#413724"), true
	}
	text = ansi.Truncate(" "+text, max(1, width), "…")
	return lipgloss.NewStyle().Background(bg).Foreground(fg).Bold(bold).Width(width).Render(text)
}

func shortImageRef(ref string) string {
	ref = strings.TrimPrefix(ref, "sha256:")
	if len(ref) > 12 {
		return ref[:12]
	}
	return ref
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func sortImages(images []domain.Image) {
	sort.Slice(images, func(i, j int) bool { return images[i].DisplayName() < images[j].DisplayName() })
}

func imageTableHeader(width int) string {
	nameWidth, sizeWidth, stateWidth, idWidth := imageColumnWidths(width)
	return imageTableCell("", 1) + " " + imageTableCell("", 3) + " " +
		imageTableCell("IMAGE", nameWidth) + "  " + imageTableCell("SIZE", sizeWidth) + "  " +
		imageTableCell("STATE", stateWidth) + "  " + imageTableCell("ID", idWidth)
}

func imageTableRow(renderer tideui.Renderer, width int, marker string, highlighted bool, selected string, image domain.Image) string {
	nameWidth, sizeWidth, stateWidth, idWidth := imageColumnWidths(width)
	state := "USED"
	stateColor := lipgloss.Color("#80c990")
	if image.Removable() {
		state = "UNUSED"
		stateColor = lipgloss.Color("#e06c75")
	}
	rowBG := renderer.Styles.Theme.Bg
	if highlighted {
		if color, ok := renderer.Styles.ItemSelected.GetBackground().(lipgloss.Color); ok {
			rowBG = color
		}
	}
	base := lipgloss.NewStyle().Background(rowBG).Foreground(styleForeground(renderer.Styles.DetailBody, renderer.Styles.Theme.Fg))
	stateCell := base.Foreground(stateColor).Bold(true).Render(imageTableCell(state, stateWidth))
	row := base.Render(imageTableCell(marker, 1)) + base.Render(" ") + base.Render(imageTableCell("["+selected+"]", 3)) + base.Render(" ") +
		base.Render(imageTableCell(image.DisplayName(), nameWidth)) + base.Render("  ") +
		base.Render(imageTableCell(formatBytes(uint64(maxInt64(0, image.Size))), sizeWidth)) + base.Render("  ") +
		stateCell + base.Render("  ") + base.Render(imageTableCell(image.ShortID(), idWidth))
	return lipgloss.NewStyle().Background(rowBG).Width(width).Render(row)
}

func imageColumnWidths(width int) (name, size, state, id int) {
	size, state, id = 8, 6, 12
	name = max(18, width-1-1-3-3-2-2-size-state-id)
	return name, size, state, id
}

func imageTableCell(value string, width int) string {
	value = ansi.Truncate(value, width, "…")
	if gap := width - lipgloss.Width(value); gap > 0 {
		value += strings.Repeat(" ", gap)
	}
	return value
}
