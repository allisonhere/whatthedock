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

// TestFormatEnvEntriesQuotesLeadingOrTrailingWhitespace is the regression
// test for a confirmed bug: formatEnvEntries only quoted an entry
// containing a comma, newline, or double quote. An entry whose value has
// leading or trailing whitespace of its own ("KEY= leading",
// "KEY=trailing ") wasn't quoted, so joining it with ", " put that
// whitespace right next to the separator — indistinguishable from the
// separator's own formatting space — and splitEnvEntries' TrimSpace on the
// next parse silently ate it. Quoting whenever the entry isn't already
// whitespace-trimmed (same condition splitEnvEntries uses to decide
// whether to trim) fixes it, the same way a comma already forces quoting.
func TestFormatEnvEntriesQuotesLeadingOrTrailingWhitespace(t *testing.T) {
	tests := []struct {
		name  string
		entry string
		want  string
	}{
		// A space right after "=" never touches the outer string's own
		// leading/trailing edge (the entry still starts with "KEY" and
		// ends with the last value rune), so it survives splitEnvEntries'
		// TrimSpace untouched even unquoted — no quoting needed.
		{name: "leading space in value", entry: "KEY= leading", want: "KEY= leading"},
		{name: "trailing space in value", entry: "KEY=trailing ", want: `"KEY=trailing "`},
		{name: "both leading and trailing", entry: "KEY=  both  ", want: `"KEY=  both  "`},
		{name: "internal space only, not quoted", entry: "KEY=value with spaces", want: "KEY=value with spaces"},
		{name: "no whitespace at all, not quoted", entry: "KEY=value", want: "KEY=value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatEnvEntries([]string{tt.entry})
			if got != tt.want {
				t.Fatalf("formatEnvEntries([]string{%q}) = %q, want %q", tt.entry, got, tt.want)
			}
		})
	}
}

// TestEnvEntriesRoundTripPreservesEdgeWhitespaceInValue exercises the full
// table from the audit: every one of these must come back byte-for-byte
// identical after formatEnvEntries -> splitEnvEntries, since none of this
// whitespace is structural (structural whitespace only ever lives between
// entries, around a comma — see formatEnvEntries' own doc comment).
func TestEnvEntriesRoundTripPreservesEdgeWhitespaceInValue(t *testing.T) {
	original := []string{
		"KEY=value",
		"KEY=",
		"KEY=a=b=c",
		"KEY=value with spaces",
		"KEY= leading",
		"KEY=trailing ",
		"KEY=  both  ",
		"EMPTY=",
	}
	roundTripped := splitEnvEntries(formatEnvEntries(original))
	if !reflect.DeepEqual(roundTripped, original) {
		t.Fatalf("round trip = %#v, want %#v", roundTripped, original)
	}
}

// TestParseCreateEnvTableFromAudit checks parseCreateEnv's behavior on the
// exact forms the audit asked about, including the one case with no "="
// (NO_VALUE) that must still be rejected — the parser needs a KEY=VALUE
// shape to identify the variable name at all, independent of the
// whitespace-preservation fix above.
//
// Trailing whitespace typed raw and unquoted is still trimmed here — that
// is intentional, pre-existing, structural behavior, not the bug: a space
// sitting at the very edge of an unquoted entry is indistinguishable from
// "KEY1=a, KEY2=b"'s own separator formatting (the space after the comma),
// so splitEnvEntries treats it as such unless the entry is quoted. The
// fix (formatEnvEntries, above) is what keeps that whitespace from ever
// silently being lost on a round trip — a value that already has it
// prefilled from a real container now comes back quoted, and quoted
// entries are never trimmed. A user hand-typing an unquoted trailing space
// with no round trip involved was never guaranteed to keep it, before or
// after this fix.
func TestParseCreateEnvTableFromAudit(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    []string
		wantErr bool
	}{
		{name: "plain value", value: "KEY=value", want: []string{"KEY=value"}},
		{name: "empty value", value: "KEY=", want: []string{"KEY="}},
		{name: "multiple equals in value", value: "KEY=a=b=c", want: []string{"KEY=a=b=c"}},
		{name: "spaces in value", value: "KEY=value with spaces", want: []string{"KEY=value with spaces"}},
		{name: "leading space in value survives unquoted", value: "KEY= leading", want: []string{"KEY= leading"}},
		{name: "trailing space in value trimmed when unquoted", value: "KEY=trailing ", want: []string{"KEY=trailing"}},
		{name: "both leading and trailing, trailing trimmed", value: "KEY=  both  ", want: []string{"KEY=  both"}},
		{name: "quoted entry preserves trailing space", value: `"KEY=trailing "`, want: []string{"KEY=trailing "}},
		{name: "empty-named key with empty value", value: "EMPTY=", want: []string{"EMPTY="}},
		{name: "no equals sign at all is rejected", value: "NO_VALUE", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCreateEnv(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseCreateEnv(%q) error = nil, want an error", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCreateEnv(%q) error = %v", tt.value, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseCreateEnv(%q) = %#v, want %#v", tt.value, got, tt.want)
			}
		})
	}
}
