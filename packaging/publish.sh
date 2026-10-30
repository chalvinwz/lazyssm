#!/bin/sh
# publish.sh: create or update one file in a GitHub repo through the Contents
# API. release.yml uses it to publish the Homebrew formula and Scoop manifest:
#
#   GH_TOKEN=<token> packaging/publish.sh <owner/repo> <path> <local-file> <commit-message>
#
# A file that already matches is not rewritten, so re-running a release job
# makes no new commit. Otherwise one PUT creates the file (after a 404) or
# updates it by blob sha. Any other API error or failed write exits non-zero.
#
# Needs gh (authenticated by GH_TOKEN), mktemp, sed, grep, cmp, tr and base64.
# Only plain base64 encode and -d are used, which both GNU coreutils (the
# release runner) and macOS (where the tests also run) support.

set -eu

usage="usage: GH_TOKEN=<token> $0 <owner/repo> <path> <local-file> <commit-message>"

die() {
	printf 'publish: %s\n' "$*" >&2
	exit 1
}

if [ $# -ne 4 ]; then
	die "$usage"
fi
repo=$1
path=$2
file=$3
message=$4
if [ -z "$repo" ] || [ -z "$path" ] || [ -z "$message" ]; then
	die "$usage"
fi
if [ -z "${GH_TOKEN:-}" ]; then
	die "GH_TOKEN is not set; it must hold a token that can write to $repo"
fi
if [ ! -f "$file" ] || [ ! -r "$file" ]; then
	die "cannot read $file"
fi
if [ ! -s "$file" ]; then
	die "$file is empty; refusing to publish an empty $path"
fi

tmp_parent=${TMPDIR:-/tmp}
tmp=$(mktemp -d "${tmp_parent%/}/publish.XXXXXX") || die "could not create a temp dir"
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' HUP INT TERM

endpoint="repos/$repo/contents/$path"

# Read the blob sha, encoding and content (base64 wrapped at 60 columns).
# On an HTTP error gh exits non-zero and prints "(HTTP <code>)" on stderr; a
# 404 means there is no file yet, or the repo is empty.
sha=""
if gh api "$endpoint" --jq '.sha, .encoding, .content' >"$tmp/current" 2>"$tmp/err"; then
	sha=$(sed -n 1p "$tmp/current")
	encoding=$(sed -n 2p "$tmp/current")
	if [ -z "$sha" ]; then
		die "reading $repo/$path: the response has no sha"
	fi
	if [ "$encoding" != base64 ]; then
		die "reading $repo/$path: content encoding is '$encoding', not base64"
	fi
	sed 1,2d "$tmp/current" | base64 -d >"$tmp/decoded" ||
		die "reading $repo/$path: content is not valid base64"
	if cmp -s "$tmp/decoded" "$file"; then
		printf '%s\n' "unchanged: $repo/$path already matches $file"
		exit 0
	fi
elif grep -q '(HTTP 404)' "$tmp/err"; then
	: # nothing there yet; the PUT below creates it
else
	cat "$tmp/err" >&2
	die "could not read $repo/$path"
fi

base64 <"$file" >"$tmp/encoded" || die "could not base64-encode $file"
content=$(tr -d '\n' <"$tmp/encoded")

# -f sends each field as a JSON string; -F would turn some values into
# numbers or booleans.
if [ -n "$sha" ]; then
	action=updated
	set -- -f "sha=$sha"
else
	action=created
	set --
fi
gh api -X PUT "$endpoint" -f "message=$message" -f "content=$content" "$@" --silent ||
	die "could not write $repo/$path"
printf '%s\n' "$action: $repo/$path ($message)"
