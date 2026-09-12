package ui

import (
	"fmt"
	"os"

	"github.com/allisonhere/tideui"
	tea "github.com/charmbracelet/bubbletea"
)

// openTTY opens the controlling terminal for control sequences without
// interleaving writes with Bubble Tea's renderer-owned stdout.
func openTTY() (*os.File, error) {
	return os.OpenFile("/dev/tty", os.O_WRONLY, 0)
}

// terminalColorSequences sets and restores the terminal's default foreground
// and background. The foreground matters anywhere an ANSI reset exposes the
// terminal default instead of a Lip Gloss style.
func terminalColorSequences(theme tideui.Theme) (set string, reset string) {
	if theme.Fg != "" {
		set += fmt.Sprintf("\x1b]10;%s\x07", string(theme.Fg))
		reset += "\x1b]110\x07"
	}
	if theme.Bg != "" {
		set += fmt.Sprintf("\x1b]11;%s\x07", string(theme.Bg))
		reset += "\x1b]111\x07"
	}
	return set, reset
}

// TerminalColorSequences returns the controls for the model's active theme so
// main can apply them before Bubble Tea starts and restore them after it exits.
func (m Model) TerminalColorSequences() (set string, reset string) {
	return terminalColorSequences(m.theme)
}

func setTermColorsCmd(theme tideui.Theme) tea.Cmd {
	return func() tea.Msg {
		if tty, err := openTTY(); err == nil {
			set, _ := terminalColorSequences(theme)
			_, _ = fmt.Fprint(tty, set)
			_ = tty.Close()
		}
		return nil
	}
}
