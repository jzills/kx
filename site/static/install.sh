#!/bin/sh
# Installs kx — https://github.com/jzills/kx
#
#   curl -fsSL https://jzills.github.io/kx/install.sh | sh
#
# Downloads the newest kx for Linux or macOS (amd64 or arm64), checks it
# against the release's SHA256SUMS, and installs it to ~/.local/bin. It never
# runs sudo.
#
#   KX_INSTALL_DIR  where to install (default: $HOME/.local/bin)
#   KX_VERSION      install this release instead of the newest, e.g. v0.7.0
#
# Everything below is a function until the last line, so a download cut off
# partway through defines functions and runs nothing.

set -eu

RELEASES_PAGE=https://github.com/jzills/kx/releases

say() {
	printf '%s\n' "$*"
}

fail() {
	printf 'kx install: %s\n' "$*" >&2
	exit 1
}

has() {
	command -v "$1" >/dev/null 2>&1
}

need_tools() {
	if has curl; then
		FETCH=curl
	elif has wget; then
		FETCH=wget
	else
		fail "needs curl or wget to download kx; install one and rerun"
	fi
	if has sha256sum; then
		SHA=sha256sum
	elif has shasum; then
		SHA="shasum -a 256"
	else
		fail "needs sha256sum or shasum to verify the download; install one and rerun"
	fi
	for tool in tar uname mktemp mkdir cp chmod mv rm; do
		has "$tool" || fail "needs $tool; install it and rerun"
	done
}

unsupported() {
	fail "no kx build for $1. Builds exist for Linux and macOS on amd64 and arm64 ($RELEASES_PAGE); elsewhere, install with: uv tool install kx-cli"
}

detect_platform() {
	kernel=$(uname -s)
	machine=$(uname -m)
	case "$kernel" in
	Linux) OS=linux ;;
	Darwin) OS=darwin ;;
	*) unsupported "$kernel/$machine" ;;
	esac
	case "$machine" in
	x86_64 | amd64) ARCH=amd64 ;;
	aarch64 | arm64) ARCH=arm64 ;;
	*) unsupported "$kernel/$machine" ;;
	esac
	# A shell under Rosetta reports x86_64 on Apple silicon; the native build
	# is the one to install.
	if [ "$OS" = darwin ] && [ "$ARCH" = amd64 ] && has sysctl &&
		[ "$(sysctl -n hw.optional.arm64 2>/dev/null)" = 1 ]; then
		ARCH=arm64
	fi
}

choose_release() {
	base=${KX_INSTALL_BASE_URL:-$RELEASES_PAGE}
	version=${KX_VERSION:-}
	if [ -z "$version" ]; then
		ARCHIVE="kx_${OS}_${ARCH}.tar.gz"
		URL_DIR="$base/latest/download"
		LABEL="the newest kx"
	else
		case "$version" in
		v*) ;;
		*) version="v$version" ;;
		esac
		ARCHIVE="kx_${version}_${OS}_${ARCH}.tar.gz"
		URL_DIR="$base/download/$version"
		LABEL="kx $version"
	fi
}

fetch() {
	if [ "$FETCH" = curl ]; then
		curl -fsSL -o "$2" "$1" || fail "could not download $1"
	else
		wget -q -O "$2" "$1" || fail "could not download $1"
	fi
}

verify() {
	expected=""
	while read -r sum name; do
		if [ "$name" = "$ARCHIVE" ]; then
			expected=$sum
		fi
	done <"$1/SHA256SUMS"
	[ -n "$expected" ] || fail "$ARCHIVE is not listed in the release's SHA256SUMS; refusing to install it"
	# shellcheck disable=SC2086 # SHA is a command and its flags, split on purpose
	actual=$($SHA "$1/$ARCHIVE")
	[ "${actual%% *}" = "$expected" ] || fail "checksum mismatch for $ARCHIVE; refusing to install it"
}

install_kx() {
	DIR=${KX_INSTALL_DIR:-$HOME/.local/bin}
	hint="set KX_INSTALL_DIR to a directory you can write, or rerun with sudo for a system directory"
	mkdir -p "$DIR" 2>/dev/null || fail "cannot create $DIR; $hint"
	[ -w "$DIR" ] || fail "cannot write to $DIR; $hint"
	tar -xzf "$1/$ARCHIVE" -C "$1" kx/kx || fail "could not unpack $ARCHIVE"
	# Through a temp file and mv: overwriting a kx that is running fails with
	# "text file busy" on Linux, and a rename does not.
	cp "$1/kx/kx" "$DIR/.kx.$$" || fail "cannot write to $DIR; $hint"
	chmod 0755 "$DIR/.kx.$$"
	mv -f "$DIR/.kx.$$" "$DIR/kx"
}

path_hint() {
	case "${SHELL:-}" in
	*/fish)
		say ""
		say "$DIR is not on your PATH. To add it, run:"
		say "  fish_add_path $DIR"
		return
		;;
	*/zsh) profile="$HOME/.zshrc" ;;
	*/bash) profile="$HOME/.bashrc" ;;
	*) profile="your shell's profile" ;;
	esac
	say ""
	say "$DIR is not on your PATH. To add it, put this line in $profile:"
	say "  export PATH=\"$DIR:\$PATH\""
}

report() {
	line=$("$DIR/kx" --version 2>/dev/null | {
		first=""
		read -r first || true
		printf '%s' "$first"
	})
	[ -n "$line" ] || fail "installed $DIR/kx, but it did not run"
	say "✓ Installed $line to $DIR/kx"
	case ":$PATH:" in
	*":$DIR:"*) ;;
	*) path_hint ;;
	esac
}

main() {
	need_tools
	detect_platform
	choose_release
	tmp=$(mktemp -d) || fail "could not create a temporary directory"
	trap 'rm -rf "$tmp"' EXIT
	trap 'exit 1' INT TERM
	say "Downloading $LABEL for $OS/$ARCH…"
	fetch "$URL_DIR/$ARCHIVE" "$tmp/$ARCHIVE"
	fetch "$URL_DIR/SHA256SUMS" "$tmp/SHA256SUMS"
	verify "$tmp"
	install_kx "$tmp"
	report
}

main "$@"
