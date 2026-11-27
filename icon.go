package whatthedock

import _ "embed"

// Application icon, as installed by `whatthedock install-desktop`. Both are
// generated from assets/icon-source.png (see `make icons`).
var (
	//go:embed assets/icon-256.png
	Icon256 []byte

	//go:embed assets/icon-512.png
	Icon512 []byte
)
