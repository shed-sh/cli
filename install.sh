#!/bin/sh
# Install the shed CLI.
#
#   curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install.sh | sh
#
# This is the same installer as install-cli.sh. Skills are a separate
# install: install-skills.sh --global or install-skills.sh --local.
set -eu

BASE_URL="https://raw.githubusercontent.com/shed-sh/cli/main"

sibling="$(dirname -- "$0" 2>/dev/null)/install-cli.sh"
if [ -f "$sibling" ]; then
	sh "$sibling" "$@"
	exit $?
fi

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
curl --proto '=https' --proto-redir '=https' -fsSL "$BASE_URL/install-cli.sh" -o "$tmp" || {
	echo "install.sh: could not download install-cli.sh" >&2
	exit 1
}
sh "$tmp" "$@"
