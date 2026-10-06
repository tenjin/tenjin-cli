#!/bin/sh
# Installs the tenjin command-line tool from its GitHub releases.
#
#   curl -fsSL https://raw.githubusercontent.com/tenjin/tenjin-cli/main/install.sh | sh
#
# TENJIN_VERSION       a version to install, such as 0.1.0 (default: the latest)
# TENJIN_INSTALL_DIR   where to put the binary (default: /usr/local/bin if it
#                      can be written to, otherwise ~/.local/bin)
#
# The archive is checked against the release's checksums.txt before anything
# is installed. Nothing is run with sudo.
set -eu

releases="${TENJIN_RELEASES_URL:-https://github.com/tenjin/tenjin-cli/releases}"

fail() {
	echo "tenjin install: $*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || fail "this script needs $1"
}

need curl
need tar
need uname

case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) fail "no installer for $(uname -s). On Windows, download the zip from $releases" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "no build for the $(uname -m) processor" ;;
esac

version="${TENJIN_VERSION:-}"
if [ -z "$version" ]; then
	# The latest release redirects to its tag's page.
	latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$releases/latest") ||
		fail "could not find the latest release at $releases"
	version="${latest##*/}"
fi
version="${version#v}"
case "$version" in
"" | latest) fail "could not tell which version is the latest" ;;
esac

archive="tenjin_${version}_${os}_${arch}.tar.gz"
url="$releases/download/v$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading tenjin $version for $os/$arch"
curl -fsSL -o "$tmp/$archive" "$url/$archive" || fail "could not download $url/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$url/checksums.txt" || fail "could not download $url/checksums.txt"

want=$(awk -v name="$archive" '$2 == name { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "$archive is not listed in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
else
	need shasum
	got=$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')
fi
[ "$got" = "$want" ] || fail "the download does not match its checksum; nothing was installed"

tar -xzf "$tmp/$archive" -C "$tmp" tenjin || fail "the archive has no tenjin binary"

dir="${TENJIN_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
	if [ -w /usr/local/bin ]; then
		dir=/usr/local/bin
	else
		dir="$HOME/.local/bin"
	fi
fi
mkdir -p "$dir"
# Write next to the target and rename, so a running tenjin is never left
# half overwritten.
cp "$tmp/tenjin" "$dir/.tenjin.new"
chmod 0755 "$dir/.tenjin.new"
mv -f "$dir/.tenjin.new" "$dir/tenjin"

echo "Installed $("$dir/tenjin" version) in $dir"
case ":$PATH:" in
*":$dir:"*) ;;
*) echo "$dir is not on your PATH. Add it, for example: export PATH=\"$dir:\$PATH\"" ;;
esac
echo "Start with: tenjin setup"
