#!/usr/bin/env bash
# Runs WhatTheDock against the real Docker daemon on jarvis (192.168.86.74)
# over an SSH-forwarded unix socket, since this SDK doesn't speak DOCKER_HOST=ssh://.
set -euo pipefail

JARVIS_USER="allie"
JARVIS_HOST="192.168.86.74"
SOCK="/tmp/jarvis-docker.sock"

cd "$(dirname "$0")"

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
DOCKER_HOST="unix://$SOCK" go run -buildvcs=false ./cmd/whatthedock "$@"
