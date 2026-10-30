#!/bin/sh
# install.sh: install lazyssm on macOS or Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/chalvinwz/lazyssm/main/install.sh | sh
#
# The archive is verified against the release's checksums.txt before anything
# is installed. sudo is used only for the mkdir and copy, and only when the
# install dir is not writable. Shell rc files are never edited, and nothing is
# read from stdin, so the script works when piped into sh.
#
# Environment:
#   LAZYSSM_VERSION        release to install, e.g. v0.3.0 or 0.3.0 (default: latest)
#   LAZYSSM_INSTALL_DIR    where to put the binary (default: /usr/local/bin)
#   LAZYSSM_DOWNLOAD_BASE  https base URL of the release downloads, or a local
#                          directory laid out as <tag>/<file> (used by tests)
#                          (default: https://github.com/chalvinwz/lazyssm/releases/download)

set -eu

REPO_URL="https://github.com/chalvinwz/lazyssm"
SCRIPT_URL="https://raw.githubusercontent.com/chalvinwz/lazyssm/main/install.sh"
INSTALL_PS1_URL="https://raw.githubusercontent.com/chalvinwz/lazyssm/main/install.ps1"
AWS_CLI_DOC_URL="https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html"
SSM_PLUGIN_DOC_URL="https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html"

tmp=""

say() {
	printf '%s\n' "$*"
}

warn() {
	printf 'warning: %s\n' "$*" >&2
}

# die prints error $1 and each further argument as a hint line, then exits 1.
die() {
	printf 'error: %s\n' "$1" >&2
	shift
	for line in "$@"; do
		printf '  %s\n' "$line" >&2
	done
	exit 1
}

has() {
	command -v "$1" >/dev/null 2>&1
}

cleanup() {
	if [ -n "$tmp" ]; then
		rm -rf "$tmp"
	fi
}

check_tools() {
	for tool in uname tar mktemp install awk; do
		has "$tool" || die "$tool is required but was not found on PATH"
	done
	if ! has sha256sum && ! has shasum; then
		die "sha256sum or shasum is required to verify the download"
	fi
}

# detect_platform sets os and arch as the release asset names spell them.
detect_platform() {
	sys=$(uname -s)
	machine=$(uname -m)
	case $sys in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	MINGW* | MSYS* | CYGWIN* | Windows_NT)
		die "install.sh is for macOS and Linux; this is Windows ($sys)" \
			"in PowerShell, use install.ps1 instead:" \
			"  irm $INSTALL_PS1_URL | iex" \
			"or install with Scoop; see $REPO_URL"
		;;
	*) die "unsupported operating system: $sys (lazyssm runs on macOS, Linux and Windows)" ;;
	esac
	case $machine in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) die "unsupported architecture: $machine (lazyssm is built for amd64 and arm64)" ;;
	esac
}

# check_source sets base, where release files come from: a local directory
# (the test seam), or an https URL fetched with curl or wget.
check_source() {
	base=${LAZYSSM_DOWNLOAD_BASE:-$REPO_URL/releases/download}
	base=${base%/}
	if [ -d "$base" ]; then
		return 0
	fi
	case $base in
	https://*) ;;
	*) die "LAZYSSM_DOWNLOAD_BASE must be an https:// URL or an existing directory, not '$base'" ;;
	esac
	if ! has curl && ! has wget; then
		die "curl or wget is required to download lazyssm"
	fi
}

# latest_version sets version from the tag that releases/latest redirects to
# (no API call, so no rate limit).
latest_version() {
	url="$REPO_URL/releases/latest"
	say "Looking up the latest release"
	if has curl; then
		final=$(curl -fsSLI --proto '=https' --tlsv1.2 -o /dev/null -w '%{url_effective}' "$url") || final=""
	elif has wget; then
		final=$(wget -S --spider "$url" 2>&1 | tr -d '\r' |
			awk 'tolower($1) == "location:" { loc = $2 } END { print loc }') || final=""
	else
		die "curl or wget is required to look up the latest release"
	fi
	case $final in
	*/releases/tag/*) version=${final##*/releases/tag/} ;;
	*) die "could not look up the latest release at $url" \
		"set LAZYSSM_VERSION (e.g. v0.3.0) to pick a release" ;;
	esac
}

# resolve_version sets version (no leading v) and tag (with it).
resolve_version() {
	version=${LAZYSSM_VERSION:-}
	if [ -z "$version" ]; then
		latest_version
	fi
	requested=$version
	version=${version#v}
	case $version in
	"" | [!0-9]* | *[!0-9A-Za-z.-]*)
		die "invalid version '$requested' (expected a release tag such as v0.3.0)"
		;;
	esac
	tag="v$version"
}

# resolve_dir sets dir, the absolute install dir without trailing slashes.
resolve_dir() {
	dir=${LAZYSSM_INSTALL_DIR:-/usr/local/bin}
	case $dir in
	# A quoted ~, as in LAZYSSM_INSTALL_DIR="~/bin", is not expanded and would
	# install into ./~/bin.
	\~*)
		die "LAZYSSM_INSTALL_DIR '$dir' starts with a literal ~; use \$HOME instead" \
			"for example: LAZYSSM_INSTALL_DIR=\"\$HOME/.local/bin\""
		;;
	/*) ;;
	*) dir="$(pwd)/$dir" ;;
	esac
	while :; do
		case $dir in
		?*/) dir=${dir%/} ;;
		*) break ;;
		esac
	done
}

# fetch copies release file $1 into the temp dir.
fetch() {
	if [ -d "$base" ]; then
		cp "$base/$tag/$1" "$tmp/$1" || die "could not get $1 for $tag from $base"
	elif has curl; then
		curl -fsSL --proto '=https' --tlsv1.2 -o "$tmp/$1" "$base/$tag/$1" ||
			die "could not download $base/$tag/$1"
	else
		wget -q -O "$tmp/$1" "$base/$tag/$1" || die "could not download $base/$tag/$1"
	fi
}

# sha256_of prints the hex SHA-256 of file $1.
sha256_of() {
	if has sha256sum; then
		sha256sum <"$1"
	else
		shasum -a 256 <"$1"
	fi | awk '{ print $1 }'
}

# verify_checksum aborts unless the archive matches its exact-name line in
# checksums.txt.
verify_checksum() {
	expected=$(awk -v name="$archive" '$2 == name { print $1; exit }' "$tmp/checksums.txt")
	if [ -z "$expected" ]; then
		die "checksums.txt for $tag has no entry for $archive; not installing"
	fi
	actual=$(sha256_of "$tmp/$archive")
	if [ "$actual" != "$expected" ]; then
		die "checksum mismatch for $archive; not installing" \
			"expected sha256 $expected" \
			"got sha256      $actual"
	fi
	say "Verified sha256 checksum"
}

extract() {
	mkdir "$tmp/extract"
	tar -xzf "$tmp/$archive" -C "$tmp/extract" || die "could not extract $archive"
	if [ ! -f "$tmp/extract/lazyssm" ]; then
		die "$archive has no lazyssm binary at its root"
	fi
}

# nearest_dir prints the closest existing directory at or above absolute path
# $1: the one whose write permission decides whether $1 can be created.
nearest_dir() {
	d=$1
	while [ ! -d "$d" ]; do
		d=${d%/*}
		d=${d:-/}
	done
	printf '%s\n' "$d"
}

require_sudo() {
	if [ "$(id -u)" = 0 ]; then
		die "$dir is not writable, even as root" \
			"set LAZYSSM_INSTALL_DIR to a writable directory"
	fi
	if ! has sudo; then
		die "$dir is not writable and sudo is not available" \
			"install where you can write instead, for example:" \
			"  curl -fsSL $SCRIPT_URL | LAZYSSM_INSTALL_DIR=\"\$HOME/.local/bin\" sh"
	fi
}

# install_binary puts lazyssm into dir, using sudo only for the mkdir and copy
# when dir (or the parent it would be created in) is not writable.
install_binary() {
	target="$dir/lazyssm"
	if [ -w "$(nearest_dir "$dir")" ]; then
		if [ ! -d "$dir" ]; then
			mkdir -p "$dir"
		fi
		install -m 0755 "$tmp/extract/lazyssm" "$target"
		return 0
	fi
	require_sudo
	say "$dir is not writable; using sudo to install lazyssm there"
	if [ ! -d "$dir" ]; then
		sudo mkdir -p "$dir" || die "sudo could not create $dir"
	fi
	sudo install -m 0755 "$tmp/extract/lazyssm" "$target" || die "sudo could not install $target"
}

report() {
	say "Installed $target"
	if installed_version=$("$target" --version 2>/dev/null); then
		say "$installed_version"
	else
		warn "'$target --version' failed; the binary may not run on this system"
	fi
}

check_path() {
	case ":${PATH:-}:" in
	*":$dir:"*) ;;
	*) warn "$dir is not on your PATH; add it in your shell profile, or run $target directly" ;;
	esac
	first=$(command -v lazyssm 2>/dev/null) || first=""
	if [ -n "$first" ] && [ "$first" != "$target" ]; then
		warn "$first comes first on your PATH and will run instead of $target; remove it or reorder PATH"
	fi
}

check_aws_tools() {
	if ! has aws; then
		warn "aws (AWS CLI v2) not found; lazyssm needs it: $AWS_CLI_DOC_URL"
	fi
	if ! has session-manager-plugin; then
		warn "session-manager-plugin not found; lazyssm needs it: $SSM_PLUGIN_DOC_URL"
	fi
}

main() {
	check_tools
	detect_platform
	check_source
	resolve_version
	resolve_dir
	archive="lazyssm_${version}_${os}_${arch}.tar.gz"
	say "Installing lazyssm $tag ($os/$arch) into $dir"

	tmp_parent=${TMPDIR:-/tmp}
	tmp=$(mktemp -d "${tmp_parent%/}/lazyssm.XXXXXX") || die "could not create a temp dir"
	say "Downloading $archive"
	fetch checksums.txt
	fetch "$archive"
	verify_checksum
	extract
	install_binary

	report
	check_path
	check_aws_tools
}

trap cleanup EXIT
trap 'exit 1' HUP INT TERM
main "$@"
