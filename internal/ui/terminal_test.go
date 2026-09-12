package ui

import (
	"testing"

	"github.com/allisonhere/tideui"
)

func TestTerminalColorSequencesSetAndResetBothDefaults(t *testing.T) {
	set, reset := terminalColorSequences(tideui.Theme{Fg: "#abcdef", Bg: "#123456"})
	if want := "\x1b]10;#abcdef\x07\x1b]11;#123456\x07"; set != want {
		t.Fatalf("set = %q, want %q", set, want)
	}
	if want := "\x1b]110\x07\x1b]111\x07"; reset != want {
		t.Fatalf("reset = %q, want %q", reset, want)
	}
}
