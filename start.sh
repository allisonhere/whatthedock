#!/usr/bin/env bash
# Runs WhatTheDock against your real local Docker daemon (whatever
# DOCKER_HOST already resolves to in your shell — the Docker default if
# unset). To instead point it at a remote host over SSH, use the app's own
# Systems overlay (a) rather than this script.
#
# --test-update: an ordinary run always reports version "dev" (only
# cmd/release's real release build sets it via ldflags), and that's never
# eligible for an update by design (see cmd/whatthedock's -fake-version
# flag). This shortcuts to -fake-version=v0.1.0 so the update-check flow
# (automatic on launch, and Settings > Check for update) has an old-enough
# version to actually offer an update over. Anything else you pass through
# (including your own -fake-version=vX.Y.Z) is forwarded to whatthedock
# unchanged.
set -euo pipefail

cd "$(dirname "$0")"

args=()
for arg in "$@"; do
	if [ "$arg" = "--test-update" ]; then
		args+=("-fake-version=v0.1.0")
	else
		args+=("$arg")
	fi
done

exec go run -buildvcs=false ./cmd/whatthedock "${args[@]}"
