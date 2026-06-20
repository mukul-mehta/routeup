#!/bin/sh
# routeup installer.
#
#   curl -fsSL https://get.routeup.dev | sh
#
# Downloads the latest release binary for your OS/arch from GitHub, verifies
# its checksum, and installs it. No sudo: lands in /usr/local/bin if writable,
# otherwise ~/.local/bin. Override with ROUTEUP_INSTALL_DIR=/path.
set -eu

REPO="mukul-mehta/routeup"
BIN="routeup"

# --- detect os/arch ---
os=$(uname -s)
arch=$(uname -m)

case "$os" in
	Darwin) os="darwin" ;;
	Linux) os="linux" ;;
	*)
		echo "routeup: unsupported OS '$os' (macOS and Linux only)" >&2
		exit 1
		;;
esac

case "$arch" in
	x86_64 | amd64) arch="amd64" ;;
	arm64 | aarch64) arch="arm64" ;;
	*)
		echo "routeup: unsupported architecture '$arch'" >&2
		exit 1
		;;
esac

asset="routeup_${os}_${arch}.tar.gz"
base="https://github.com/${REPO}/releases/latest/download"

# --- pick an install dir (no sudo) ---
if [ -n "${ROUTEUP_INSTALL_DIR:-}" ]; then
	dir="$ROUTEUP_INSTALL_DIR"
elif [ -w /usr/local/bin ]; then
	dir="/usr/local/bin"
else
	dir="$HOME/.local/bin"
fi
mkdir -p "$dir"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "routeup: downloading $asset ..."
curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

# --- verify checksum ---
echo "routeup: verifying checksum ..."
want=$(grep " ${asset}\$" "$tmp/checksums.txt" | awk '{print $1}')
if [ -z "$want" ]; then
	echo "routeup: no checksum for $asset in checksums.txt" >&2
	exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$asset" | awk '{print $1}')
else
	got=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
fi
if [ "$want" != "$got" ]; then
	echo "routeup: checksum mismatch for $asset" >&2
	echo "  want $want" >&2
	echo "  got  $got" >&2
	exit 1
fi

# --- extract + install ---
tar -xzf "$tmp/$asset" -C "$tmp"
chmod 0755 "$tmp/$BIN"
mv "$tmp/$BIN" "$dir/$BIN"
echo "routeup: installed to $dir/$BIN"

case ":$PATH:" in
	*":$dir:"*) ;;
	*)
		echo ""
		echo "routeup: $dir is not on your PATH. Add it:"
		echo "  export PATH=\"$dir:\$PATH\""
		;;
esac

echo ""
echo "next: run 'routeup setup' to create and trust the local CA."
