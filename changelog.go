// Package whatthedock exists to embed files that live at the repository root
// into the binary — the packages that need them sit in subdirectories, and
// go:embed can't reach up out of its own directory.
package whatthedock

import _ "embed"

// Changelog is CHANGELOG.md as of the build, shown in the app under
// Settings > View changelog.
//
//go:embed CHANGELOG.md
var Changelog string
