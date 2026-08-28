package domain

import "testing"

func TestSplitShellWordsPlainSpaceSeparated(t *testing.T) {
	got := SplitShellWords("sh -c date")
	want := []string{"sh", "-c", "date"}
	if !equalStrings(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestJoinThenSplitShellWordsRoundTripsArgumentWithEmbeddedSpace(t *testing.T) {
	args := []string{"sh", "-c", "echo hi"}
	joined := JoinShellWords(args)
	got := SplitShellWords(joined)
	if !equalStrings(got, args) {
		t.Fatalf("round trip of %#v via %q produced %#v", args, joined, got)
	}
}

func TestJoinThenSplitShellWordsRoundTripsArgumentWithEmbeddedQuote(t *testing.T) {
	args := []string{"echo", `say "hi" now`}
	joined := JoinShellWords(args)
	got := SplitShellWords(joined)
	if !equalStrings(got, args) {
		t.Fatalf("round trip of %#v via %q produced %#v", args, joined, got)
	}
}

func TestSplitShellWordsIgnoresExtraWhitespace(t *testing.T) {
	got := SplitShellWords("  sh   -c   date  ")
	want := []string{"sh", "-c", "date"}
	if !equalStrings(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestSplitShellWordsEmptyInputReturnsNil(t *testing.T) {
	if got := SplitShellWords("   "); got != nil {
		t.Fatalf("got %#v, want nil", got)
	}
}

func TestJoinShellWordsEmptyArgsReturnsEmptyString(t *testing.T) {
	if got := JoinShellWords(nil); got != "" {
		t.Fatalf("got %q, want empty string", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
