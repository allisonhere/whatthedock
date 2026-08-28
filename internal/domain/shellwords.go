package domain

import (
	"strings"
	"unicode"
)

// JoinShellWords and SplitShellWords are a matched pair for encoding an
// exec-form argument list (Docker's own Cmd/Entrypoint shape — one string
// per argument) into and out of the single-line, space-joined text
// several callers use to display or hand-edit a command: docker.FromInspect
// (yank/inspect), the create form's Command field (internal/ui), and
// clipboard.PortableContainer.ToCreateSpec (paste). None of them are
// interpreted by an actual shell — Docker's API takes the []string
// directly — so this is purely whatthedock's own round-trip encoding, not
// real shell quoting, and free to use whatever scheme keeps the round trip
// lossless.
//
// A plain space join (what this used to be) is lossy for any argument
// containing whitespace: ["sh","-c","echo hi"] joined as "sh -c echo hi"
// and split back on whitespace comes back as four arguments instead of
// three. JoinShellWords quotes (doubling any embedded ") whichever
// arguments need it; SplitShellWords is its inverse.

// JoinShellWords encodes args into one space-separated string, wrapping
// any argument that contains whitespace or a double quote in double
// quotes (embedded quotes doubled) so SplitShellWords can recover it as a
// single argument.
func JoinShellWords(args []string) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if strings.ContainsAny(a, " \t\n\"") {
			a = `"` + strings.ReplaceAll(a, `"`, `""`) + `"`
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// SplitShellWords is JoinShellWords' inverse: splits value on whitespace,
// except a double-quoted run is taken literally end to end (whitespace
// inside it doesn't split it, and a doubled "" inside represents one
// literal ").
func SplitShellWords(value string) []string {
	runes := []rune(value)
	n := len(runes)
	var out []string
	i := 0
	for i < n {
		for i < n && unicode.IsSpace(runes[i]) {
			i++
		}
		if i >= n {
			break
		}
		var word []rune
		if runes[i] == '"' {
			i++
			for i < n {
				if runes[i] == '"' {
					if i+1 < n && runes[i+1] == '"' {
						word = append(word, '"')
						i += 2
						continue
					}
					i++ // consume the closing quote
					break
				}
				word = append(word, runes[i])
				i++
			}
			// Ignore anything between the closing quote and the next
			// whitespace instead of erroring — malformed trailing text
			// right after a quoted word is rare enough not to be worth a
			// parse failure over.
			for i < n && !unicode.IsSpace(runes[i]) {
				i++
			}
		} else {
			start := i
			for i < n && !unicode.IsSpace(runes[i]) {
				i++
			}
			word = runes[start:i]
		}
		out = append(out, string(word))
	}
	return out
}
