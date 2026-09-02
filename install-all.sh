#!/bin/sh
# Install the shed CLI and its agent skill.
#
#   curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install-all.sh | sh
#
# Each half has its own script — install-cli.sh and install-skills.sh — and
# this one only runs both, so there is exactly one implementation of each
# install path. When this script sits next to its siblings (a checkout, or the
# published repository) it runs them directly; piped through curl it fetches
# them from the canonical URL it is itself served from.
set -eu

BASE_URL="https://raw.githubusercontent.com/shed-sh/cli/main"
CLI_ARGS=""

usage() {
	cat <<'EOF'
Install the shed CLI and its agent skill.

Usage: install-all.sh [options]

  --version <v>     Install a specific CLI version (default: latest release)
  --bin-dir <dir>   Where to put the binary (default: ~/.local/bin, or the
                    XDG bin directory when one is configured)
  -h, --help        Show this message

CLI only:   install-cli.sh
Skill only: install-skills.sh
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--version | --bin-dir)
		CLI_ARGS="$CLI_ARGS $1 ${2:?$1 needs a value}"
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "install-all.sh: unknown option $1" >&2
		usage >&2
		exit 2
		;;
	esac
done

# Locate a sibling script, or fetch it. $0 is "sh" when piped, so a failed
# dirname lookup falls through to the canonical URL.
run_part() {
	name="$1"
	shift
	sibling="$(dirname -- "$0" 2>/dev/null)/$name"
	if [ -f "$sibling" ]; then
		sh "$sibling" "$@"
		return
	fi
	tmp="$(mktemp)"
	trap 'rm -f "$tmp"' EXIT
	curl --proto '=https' --proto-redir '=https' -fsSL "$BASE_URL/$name" -o "$tmp" || {
		echo "install-all.sh: could not download $name" >&2
		exit 1
	}
	sh "$tmp" "$@"
	rm -f "$tmp"
}

# shellcheck disable=SC2086 # CLI_ARGS is a deliberate word-split flag list
run_part install-cli.sh $CLI_ARGS
run_part install-skills.sh

echo "Done. Run: shed help"
