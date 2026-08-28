package ui

import (
	"reflect"
	"testing"
)

// TestSplitEnvEntriesPreservesCommaInQuotedValue is a regression test for
// a real bug found in review: a plain comma-separated split (splitDraftList,
// what this field used before) can't tell a literal comma inside an env
// value from the separator between env vars — APP_OPTS=a,b,c used to come
// back as three fragments ("APP_OPTS=a", "b", "c"), and "b"/"c" then fail
// "must be KEY=value" validation (or silently become their own bogus vars
// if a fragment happens to contain its own "="). Quoting the whole entry
// keeps it intact.
func TestSplitEnvEntriesPreservesCommaInQuotedValue(t *testing.T) {
	value := `PUID=1000, "APP_OPTS=a,b,c", DEBUG=true`
	got := splitEnvEntries(value)
	want := []string{"PUID=1000", "APP_OPTS=a,b,c", "DEBUG=true"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitEnvEntries(%q) = %#v, want %#v", value, got, want)
	}
}

func TestSplitEnvEntriesUnquotedStillWorksLikeBefore(t *testing.T) {
	value := "PUID=1000, PGID=1000, TZ=America/Chicago"
	got := splitEnvEntries(value)
	want := []string{"PUID=1000", "PGID=1000", "TZ=America/Chicago"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitEnvEntries(%q) = %#v, want %#v", value, got, want)
	}
}

func TestSplitEnvEntriesHandlesDoubledQuoteEscape(t *testing.T) {
	// Quoting wraps the *whole* "KEY=VALUE" entry (formatEnvEntries' own
	// convention), not just the part after "=" — the opening quote comes
	// before the key.
	value := `"MSG=say ""hi"" to them"`
	got := splitEnvEntries(value)
	want := []string{`MSG=say "hi" to them`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitEnvEntries(%q) = %#v, want %#v", value, got, want)
	}
}

func TestFormatEnvEntriesQuotesOnlyWhenNeeded(t *testing.T) {
	entries := []string{"PUID=1000", "APP_OPTS=a,b,c", `MSG=say "hi"`}
	got := formatEnvEntries(entries)
	want := `PUID=1000, "APP_OPTS=a,b,c", "MSG=say ""hi"""`
	if got != want {
		t.Fatalf("formatEnvEntries(%#v) = %q, want %q", entries, got, want)
	}
}

func TestEnvEntriesRoundTripThroughFormatAndSplit(t *testing.T) {
	original := []string{"PUID=1000", "APP_OPTS=a,b,c", "CONN=host=db,port=5432", `QUOTED=has "quotes" in it`}
	roundTripped := splitEnvEntries(formatEnvEntries(original))
	if !reflect.DeepEqual(roundTripped, original) {
		t.Fatalf("round trip = %#v, want %#v", roundTripped, original)
	}
}

func TestParseCreateEnvAcceptsCommaBearingValue(t *testing.T) {
	env, err := parseCreateEnv(`PUID=1000, "APP_OPTS=a,b,c"`)
	if err != nil {
		t.Fatalf("parseCreateEnv() error = %v", err)
	}
	want := []string{"PUID=1000", "APP_OPTS=a,b,c"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("parseCreateEnv() = %#v, want %#v", env, want)
	}
}
