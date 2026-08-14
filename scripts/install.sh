#!/bin/sh
# Installs the latest whatthedock GitHub release for your platform.
#
#   curl -fsSL https://raw.githubusercontent.com/allisonhere/whatthedock/main/scripts/install.sh | sh
#
# Override the install location with INSTALL_DIR (default: /usr/local/bin,
# falling back to ~/.local/bin if that isn't writable).
set -eu

repo="allisonhere/whatthedock"

os=$(uname -s)
case "$os" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*)
		echo "install: unsupported OS '$os' (only Linux and macOS releases are published)" >&2
		exit 1
		;;
esac

arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*)
		echo "install: unsupported architecture '$arch'" >&2
		exit 1
		;;
esac

echo "install: fetching latest release info..." >&2
release_json=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest") ||
	{
		echo "install: could not reach the GitHub API to find the latest release" >&2
		exit 1
	}

# Avoids a jq dependency: tag_name is always a top-level, single-line JSON
# string field in the releases API response.
tag=$(printf '%s' "$release_json" | grep -o '"tag_name": *"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/')
if [ -z "$tag" ]; then
	echo "install: no published release found for $repo yet" >&2
	exit 1
fi

asset="whatthedock-$tag-$os-$arch"
url="https://github.com/$repo/releases/download/$tag/$asset"

install_dir=${INSTALL_DIR:-/usr/local/bin}
if [ ! -w "$install_dir" ] && [ -z "${INSTALL_DIR:-}" ]; then
	install_dir="$HOME/.local/bin"
	mkdir -p "$install_dir"
fi

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

echo "install: downloading $asset ($tag)..." >&2
if ! curl -fsSL -o "$tmp" "$url"; then
	echo "install: no release asset for $os/$arch at $url" >&2
	echo "install: this platform may not be published for $tag yet" >&2
	exit 1
fi

chmod +x "$tmp"
mv "$tmp" "$install_dir/whatthedock"
trap - EXIT

echo "install: installed whatthedock $tag to $install_dir/whatthedock" >&2
case ":$PATH:" in
	*":$install_dir:"*) ;;
	*) echo "install: $install_dir is not on your PATH — add it, e.g. export PATH=\"$install_dir:\$PATH\"" >&2 ;;
esac

"$install_dir/whatthedock" --version
