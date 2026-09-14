#!/bin/sh
# Install (or update) the sf binary from a GitHub release of sofia-ctx/sofia.
#
#   curl -fsSL https://raw.githubusercontent.com/sofia-ctx/sofia/main/install.sh | sh
#
# Environment:
#   SF_VERSION      release to install, e.g. v0.20.0 (default: latest)
#   SF_INSTALL_DIR  where to put the binary (default: $HOME/.local/bin)
#   GH_TOKEN / GITHUB_TOKEN   optional; only used to raise the GitHub API
#                   rate limit when resolving "latest" — the repo is public
#
# Linux and macOS, amd64 and arm64. Windows ships as a zip: use
# `go install github.com/sofia-ctx/sofia/cmd/sf@latest` there instead.
#
# The archive's sha256 is checked against the release's checksums.txt before
# anything is unpacked.

set -eu

REPO="sofia-ctx/sofia"
BASE_URL="${SF_BASE_URL:-https://github.com/$REPO/releases/download}"
API_URL="${SF_API_URL:-https://api.github.com/repos/$REPO/releases/latest}"
INSTALL_DIR="${SF_INSTALL_DIR:-$HOME/.local/bin}"

fail() {
	echo "install.sh: $*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || fail "$1 is required but not on PATH"
}

need curl
need tar
need uname
need mktemp
need install
need awk

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	fail "sha256sum or shasum is required to verify the download"
fi

case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) fail "unsupported OS $(uname -s); use: go install github.com/sofia-ctx/sofia/cmd/sf@latest" ;;
esac

case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) fail "unsupported architecture $(uname -m); use: go install github.com/sofia-ctx/sofia/cmd/sf@latest" ;;
esac

# Resolve the tag: explicit SF_VERSION (with or without the leading v), else
# the latest release via the API. A token is optional and only lifts the
# unauthenticated rate limit.
if [ -n "${SF_VERSION:-}" ]; then
	tag="${SF_VERSION}"
	case "$tag" in v*) ;; *) tag="v$tag" ;; esac
else
	auth=""
	token="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
	if [ -n "$token" ]; then
		auth="Authorization: Bearer $token"
	fi
	tag=$(curl -fsSL ${auth:+-H "$auth"} -H "Accept: application/vnd.github+json" "$API_URL" |
		sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
	[ -n "$tag" ] || fail "could not resolve the latest release from $API_URL; set SF_VERSION=vX.Y.Z"
fi
version="${tag#v}"

asset="sf_${version}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "Downloading sf $tag ($os/$arch)..."
curl -fsSL -o "$tmp/$asset" "$BASE_URL/$tag/$asset" ||
	fail "download failed: $BASE_URL/$tag/$asset"
curl -fsSL -o "$tmp/checksums.txt" "$BASE_URL/$tag/checksums.txt" ||
	fail "download failed: $BASE_URL/$tag/checksums.txt"

# checksums.txt is sha256sum format: "<hex>  <name>". The entry must exist
# and must match before anything is unpacked.
want=$(awk -v name="$asset" '$2 == name { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "checksums.txt of $tag has no entry for $asset"
got=$(sha256 "$tmp/$asset")
if [ "$got" != "$want" ]; then
	fail "checksum mismatch for $asset: got $got, want $want"
fi

tar -xzf "$tmp/$asset" -C "$tmp" sf
[ -f "$tmp/sf" ] || fail "$asset does not contain the sf binary"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$tmp/sf" "$INSTALL_DIR/sf"

# A bare `sf` prints its version line first.
echo "Installed $("$INSTALL_DIR/sf" 2>/dev/null | head -n 1 || echo "sf $tag") to $INSTALL_DIR/sf"
case ":$PATH:" in
	*":$INSTALL_DIR:"*) ;;
	*) echo "Note: $INSTALL_DIR is not on your PATH; add it, then run \`sf init\` and \`sf doctor\`." ;;
esac
