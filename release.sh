#!/usr/bin/env bash
# Cuts a signed WhatTheDock release — the interactive TUI (go run
# ./cmd/release, see that command's own doc comment): pick/edit the
# version, commit a dirty tree right there if needed, review the plan,
# then build/sign/tag/push/publish. Reads the signing key from
# .whatthedock-signing-key (gitignored) unless WHATTHEDOCK_SIGNING_KEY is
# already exported. Pass -auto for the old unattended path instead
# (auto-versioned, no prompts, refuses a dirty tree), or -dry-run /
# -version=vX.Y.Z / -genkey — see `go run ./cmd/release -h` equivalent in
# main.go's doc comment for the full flag list.
set -euo pipefail
cd "$(dirname "$0")"
exec go run ./cmd/release "$@"
