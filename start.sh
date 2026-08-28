#!/usr/bin/env bash
# Runs WhatTheDock against the real Docker daemon on jarvis (192.168.86.74)
# over an SSH-forwarded unix socket, since this SDK doesn't speak DOCKER_HOST=ssh://.
#
# --test-update: an ordinary run always reports version "dev" (only
# cmd/release's real release build sets it via ldflags), and that's never
# eligible for an update by design (see cmd/whatthedock's -fake-version
# flag). This shortcuts to -fake-version=v0.1.0 so the update-check flow
# (automatic on launch, and Settings > Check for update) has an old-enough
# version to actually offer an update over — against jarvis's real Docker,
# not the demo provider, so a real install/restart can be exercised too.
# Anything else you pass through (including your own -fake-version=vX.Y.Z)
# is forwarded to whatthedock unchanged.
set -euo pipefail

JARVIS_USER="allie"
JARVIS_HOST="192.168.86.74"
SOCK="/tmp/jarvis-docker.sock"

cd "$(dirname "$0")"

args=()
for arg in "$@"; do
	if [ "$arg" = "--test-update" ]; then
		args+=("-fake-version=v0.1.0")
	else
		args+=("$arg")
	fi
done

if [ ! -S "$SOCK" ] || ! DOCKER_HOST="unix://$SOCK" docker version >/dev/null 2>&1; then
	rm -f "$SOCK"
	echo "opening tunnel to $JARVIS_USER@$JARVIS_HOST..."
	ssh -fN -L "$SOCK:/var/run/docker.sock" "$JARVIS_USER@$JARVIS_HOST"
	for _ in $(seq 1 20); do
		[ -S "$SOCK" ] && break
		sleep 0.2
	done
fi

echo "connected to jarvis, launching whatthedock..."
DOCKER_HOST="unix://$SOCK" go run -buildvcs=false ./cmd/whatthedock "${args[@]}"
