package ui

import (
	"strings"
	"testing"
)

// A line over maxLogLineBytes used to fail the scanner, silently ending the
// whole log stream. It must now be chunked, and later lines must survive.
func TestLogScannerChunksOverlongLineAndContinues(t *testing.T) {
	long := strings.Repeat("x", maxLogLineBytes+10)
	scanner := newLogScanner(strings.NewReader("first\n" + long + "\nlast\n"))
	var got []string
	for scanner.Scan() {
		got = append(got, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner.Err() = %v, want nil", err)
	}
	if len(got) != 4 || got[0] != "first" || got[3] != "last" {
		t.Fatalf("lines = %d (%q ... %q), want first, two chunks, last", len(got), got[0], got[len(got)-1])
	}
	if len(got[1])+len(got[2]) != len(long) {
		t.Fatalf("chunks total %d bytes, want %d", len(got[1])+len(got[2]), len(long))
	}
}
