package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/allisonhere/tideui"
	"github.com/allisonhere/whatthedock/internal/domain"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestCuratorsKeepSelectionAndFeedbackVisible(t *testing.T) {
	for _, height := range []int{16, 24, 34} {
		for _, kind := range []string{"image", "network", "volume"} {
			t.Run(fmt.Sprintf("%s/%d", kind, height), func(t *testing.T) {
				m := testModel()
				m.width, m.height = 80, height
				for i := 0; i < 80; i++ {
					name := fmt.Sprintf("resource-%02d", i)
					m.imageItems = append(m.imageItems, domain.Image{ID: domain.ResourceID{ID: name}, RepoTags: []string{name}})
					m.networkItems = append(m.networkItems, domain.Network{ID: name, Name: name})
					m.volumeItems = append(m.volumeItems, domain.Volume{Name: name})
				}
				switch kind {
				case "image":
					m.overlay = overlayImageCuration
					m.imageError = "review failure"
				case "network":
					m.overlay = overlayNetworkCuration
					m.networkError = "review failure"
				case "volume":
					m.overlay = overlayVolumeCuration
					m.volumeError = "review failure"
				}
				renderer := tideui.NewRenderer(m.theme, tideui.StyleOptions{})
				for i := 0; i < 80; i++ {
					var panel *tideui.Overlay
					switch kind {
					case "image":
						panel = m.imageCurationOverlay(renderer)
					case "network":
						panel = m.networkCurationOverlay(renderer)
					case "volume":
						panel = m.volumeCurationOverlay(renderer)
					}
					if h := lipgloss.Height(panel.Content); h > m.height {
						t.Fatalf("panel height = %d, terminal = %d", h, m.height)
					}
					view := ansi.Strip(m.View())
					for _, want := range []string{fmt.Sprintf("resource-%02d", i), "review failure", "selected:"} {
						if !strings.Contains(view, want) {
							t.Fatalf("missing %q at row %d", want, i)
						}
					}
					updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
					m = updated.(Model)
				}
			})
		}
	}
}
