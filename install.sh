#!/bin/sh
# Installs the Tenjin CLI (the tenjin command) from its GitHub releases.
#
#   curl -fsSL https://raw.githubusercontent.com/tenjin/tenjin-cli/main/install.sh | sh
#
# TENJIN_VERSION         a version to install, such as 0.1.0 (default: the
#                        latest)
# TENJIN_INSTALL_DIR     where to put the binary (default: where tenjin already
#                        is, or a folder already on PATH that needs no sudo, or
#                        ~/.local/bin)
# TENJIN_NO_MODIFY_PATH  set to 1 to leave shell startup files alone
#
# The archive is checked against the release's checksums.txt before anything
# is installed. Nothing is run with sudo. When the folder is not on PATH yet,
# it is added in the shell startup files, so the tenjin command works in
# every new terminal without further steps.
set -eu

releases="${TENJIN_RELEASES_URL:-https://github.com/tenjin/tenjin-cli/releases}"

fail() {
	echo "Tenjin CLI installer: $*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || fail "this script needs $1"
}

need curl
need tar
need uname
[ -n "${HOME:-}" ] || fail "the HOME variable is not set, so there is no folder to install into"

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
	# With no release yet, it stays on the releases page.
	case "$latest" in
	*/tag/*) ;;
	*) fail "no release of the Tenjin CLI has been published at $releases yet" ;;
	esac
	version="${latest##*/}"
fi
version="${version#v}"
case "$version" in
"" | latest) fail "could not tell which version is the latest" ;;
esac

on_path() {
	case ":$PATH:" in
	*":$1:"*) return 0 ;;
	*) return 1 ;;
	esac
}

# The folder: where tenjin already is, so an update replaces it and no older
# copy is left ahead of it on PATH; otherwise a usual folder for programs
# that is on PATH already and needs no sudo; otherwise ~/.local/bin.
dir="${TENJIN_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
	current=$(command -v tenjin 2>/dev/null || true)
	if [ -L "$current" ]; then
		case "$(readlink "$current")" in
		*/Caskroom/* | */Cellar/*) fail "The Tenjin CLI was installed with Homebrew ($current). Update it with: brew upgrade tenjin" ;;
		esac
		# Some other manager's link: leave it, and install beside it.
		current=""
	fi
	case "$current" in
	/*) [ -w "$(dirname "$current")" ] && dir=$(dirname "$current") ;;
	esac
fi
if [ -z "$dir" ]; then
	for candidate in "$HOME/.local/bin" "$HOME/bin" /usr/local/bin; do
		if on_path "$candidate" && { [ -w "$candidate" ] || { [ ! -e "$candidate" ] && [ "$candidate" != /usr/local/bin ]; }; }; then
			dir="$candidate"
			break
		fi
	done
fi
[ -n "$dir" ] || dir="$HOME/.local/bin"
# A relative folder would put a relative entry on PATH, which runs whatever
# is at that path in any folder a shell is in.
case "$dir" in
/*) ;;
*) dir="$(pwd)/$dir" ;;
esac

archive="tenjin_${version}_${os}_${arch}.tar.gz"
url="$releases/download/v$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading the Tenjin CLI $version for $os/$arch"
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

mkdir -p "$dir" 2>/dev/null || fail "could not create $dir; nothing was installed. Set TENJIN_INSTALL_DIR to a folder you can write to"
[ -w "$dir" ] || fail "$dir cannot be written to; nothing was installed. Set TENJIN_INSTALL_DIR to a folder you can write to"
# Write next to the target and rename, so a running tenjin is never left
# half overwritten.
cp "$tmp/tenjin" "$dir/.tenjin.new" || fail "could not write to $dir; nothing was installed"
chmod 0755 "$dir/.tenjin.new"
mv -f "$dir/.tenjin.new" "$dir/tenjin" || fail "could not write to $dir; nothing was installed"

echo "Installed $("$dir/tenjin" version) in $dir"

# Adds the folder to PATH in a shell startup file, once. A file that cannot
# be written, such as one a dotfiles manager keeps read-only, is reported and
# skipped.
changed=""
add_to() {
	file="$1"
	line="$2"
	if [ -f "$file" ] && grep -qF "$line" "$file" 2>/dev/null; then
		return 0
	fi
	mkdir -p "$(dirname "$file")" 2>/dev/null || true
	if printf '\n# Added by the Tenjin CLI installer\n%s\n' "$line" >> "$file" 2>/dev/null; then
		changed="$changed $file"
	else
		echo "Could not write to $file; add this line to it yourself: $line" >&2
	fi
}

if ! on_path "$dir" && [ "${TENJIN_NO_MODIFY_PATH:-}" != 1 ]; then
	# The folder goes inside double quotes in these lines, so the characters
	# special there are escaped: a path holding a quote, a dollar sign or a
	# backquote must not run anything when a shell starts.
	sh_dir=$(printf '%s' "$dir" | sed 's/[\\"$`]/\\&/g')
	fish_dir=$(printf '%s' "$dir" | sed 's/[\\"$]/\\&/g')
	sh_line="case \":\$PATH:\" in *\":$sh_dir:\"*) ;; *) export PATH=\"$sh_dir:\$PATH\" ;; esac"
	login_shell=$(basename "${SHELL:-sh}")
	# Every zsh reads .zshenv, including the ones editors and coding agents
	# start to run a command; .zshrc is read by interactive ones only. A new
	# zsh reads the one in the home folder first, whatever ZDOTDIR is set to
	# later, so the line goes there, and in ZDOTDIR's as well when set.
	if [ "$login_shell" = zsh ] || [ -f "$HOME/.zshrc" ] || [ -f "$HOME/.zshenv" ] || [ -n "${ZDOTDIR:-}" ]; then
		add_to "$HOME/.zshenv" "$sh_line"
		if [ -n "${ZDOTDIR:-}" ] && [ "$ZDOTDIR" != "$HOME" ]; then
			add_to "$ZDOTDIR/.zshenv" "$sh_line"
		fi
	fi
	if [ "$login_shell" = bash ] || [ -f "$HOME/.bashrc" ]; then
		add_to "$HOME/.bashrc" "$sh_line"
		# A login shell, as macOS's Terminal starts, reads only the first of
		# these that exists, and not .bashrc.
		login_file="$HOME/.profile"
		for f in "$HOME/.bash_profile" "$HOME/.bash_login" "$HOME/.profile"; do
			if [ -f "$f" ]; then
				login_file="$f"
				break
			fi
		done
		add_to "$login_file" "$sh_line"
	fi
	case "$login_shell" in
	zsh | bash | fish) [ -f "$HOME/.profile" ] && add_to "$HOME/.profile" "$sh_line" ;;
	*) add_to "$HOME/.profile" "$sh_line" ;;
	esac
	fish_config="${XDG_CONFIG_HOME:-$HOME/.config}/fish"
	if [ "$login_shell" = fish ] || [ -d "$fish_config" ]; then
		add_to "$fish_config/conf.d/tenjin.fish" "contains -- \"$fish_dir\" \$PATH; or set -gx PATH \"$fish_dir\" \$PATH"
	fi
	if [ -n "$changed" ]; then
		echo "Added $dir to PATH in:$changed"
	fi
fi

# An older copy elsewhere on PATH, such as one in a folder this script could
# not write to, may be found before this one, now or in new terminals.
found=$(command -v tenjin 2>/dev/null || true)
if [ -n "$found" ] && [ "$found" != "$dir/tenjin" ]; then
	echo "Another copy of the Tenjin CLI is at $found, and may run instead of this one. Remove it (sudo rm $found if it needs it), or run $dir/tenjin."
fi
if on_path "$dir"; then
	echo "Start with: tenjin setup"
elif [ -n "$changed" ]; then
	echo "New terminals will find the tenjin command. In this one, start with: $dir/tenjin setup"
else
	echo "$dir is not on your PATH. Start with: $dir/tenjin setup"
fi
