#!/usr/bin/env bash
# Cuts a signed WhatTheDock release, unattended: build, sign, tag, push,
# publish (go run ./cmd/release -auto — see that command's own doc
# comment). Auto-suggests the next patch version unless -version=vX.Y.Z is
# passed; -dry-run walks through every step without tagging, pushing, or
# publishing. Reads the signing key from .whatthedock-signing-key
# (gitignored) unless WHATTHEDOCK_SIGNING_KEY is already exported.
set -euo pipefail
cd "$(dirname "$0")"
exec go run ./cmd/release -auto "$@"
