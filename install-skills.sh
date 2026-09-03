#!/bin/sh
# Install the shed agent skill, globally or into this project.
#
#   curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install-skills.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install-skills.sh | sh -s -- --local
#
# Global: one git clone under ~/.shed/skills, symlinked into every coding
# agent on this machine. Local: a copy in this project's .claude/skills/shed
# so the project carries the skill. git is the only requirement when the
# skill is not already sitting next to this script.
set -eu

REPO_URL="${SHED_SKILLS_REPO:-https://github.com/shed-sh/cli}"
SHED_HOME="${SHED_HOME:-$HOME/.shed}"
CLONE_DIR="$SHED_HOME/skills"
SCOPE="global"
PROJECT_DIR=""
tmp_clone=""

usage() {
	cat <<'EOF'
Install the shed agent skill.

Usage: install-skills.sh [--global | --local] [--dir <path>]

  --global   This machine: clone into ~/.shed/skills and symlink into
             every coding agent found in $HOME (default)
  --local    This project: copy into .claude/skills/shed, and into
             .cursor/skills and .codex/skills when those directories exist
  --dir <p>  Project root for --local (default: the current directory)
  -h, --help Show this message

Environment: SHED_HOME (default ~/.shed), SHED_SKILLS_REPO
EOF
}

die() {
	echo "install-skills.sh: $*" >&2
	exit 1
}

cleanup() {
	if [ -n "$tmp_clone" ]; then
		rm -rf "$tmp_clone"
	fi
}
trap cleanup EXIT

while [ $# -gt 0 ]; do
	case "$1" in
	--global)
		SCOPE="global"
		shift
		;;
	--local)
		SCOPE="local"
		shift
		;;
	--dir)
		PROJECT_DIR="${2:?--dir needs a path}"
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "install-skills.sh: unknown option $1" >&2
		usage >&2
		exit 2
		;;
	esac
done

if [ -n "$PROJECT_DIR" ] && [ "$SCOPE" != "local" ]; then
	die "--dir is only valid with --local"
fi

checkout_skill() {
	script_dir="$(dirname -- "$0" 2>/dev/null || echo .)"
	if [ -d "$script_dir/skills/shed" ]; then
		echo "$script_dir/skills/shed"
		return
	fi
	command -v git >/dev/null 2>&1 || die "git is required but was not found.
      Or copy the skill by hand: download $REPO_URL and
      cp -R skills/shed .claude/skills/shed"
	tmp_clone="$(mktemp -d)"
	git clone --depth 1 --quiet "$REPO_URL" "$tmp_clone" ||
		die "could not clone $REPO_URL"
	echo "$tmp_clone/skills/shed"
}

copy_skill() {
	src="$1"
	dest="$2"
	[ -d "$src" ] || die "$src does not contain the shed skill"
	mkdir -p "$(dirname -- "$dest")"
	rm -rf "$dest"
	cp -R "$src" "$dest"
}

install_global() {
	command -v git >/dev/null 2>&1 || die "git is required but was not found.
      Or copy the skill by hand: download $REPO_URL and
      cp -R skills/shed ~/.claude/skills/shed"

	if [ -d "$CLONE_DIR/.git" ]; then
		echo "Updating the shed skill in $CLONE_DIR"
		git -C "$CLONE_DIR" pull --ff-only --quiet ||
			die "could not update $CLONE_DIR; resolve its state or delete it and rerun"
	else
		echo "Cloning the shed skill into $CLONE_DIR"
		mkdir -p "$SHED_HOME"
		git clone --depth 1 --quiet "$REPO_URL" "$CLONE_DIR" ||
			die "could not clone $REPO_URL"
	fi

	src="$CLONE_DIR/skills/shed"
	[ -d "$src" ] || die "$CLONE_DIR does not contain skills/shed"

	linked=""
	mkdir -p "$HOME/.claude/skills"
	for agent_dir in "$HOME/.claude" "$HOME/.cursor" "$HOME/.codex"; do
		[ -d "$agent_dir" ] || continue
		mkdir -p "$agent_dir/skills"
		ln -sfn "$src" "$agent_dir/skills/shed"
		linked="$linked ${agent_dir#"$HOME"/}"
	done

	echo "Installed the shed skill for:$linked"
	echo "Rerun this script any time to update it."
}

install_local() {
	root="${PROJECT_DIR:-.}"
	[ -d "$root" ] || die "no directory named $root"
	root="$(CDPATH= cd -- "$root" && pwd)"

	src="$(checkout_skill)"
	[ -d "$src" ] || die "could not find skills/shed in the source tree"

	copy_skill "$src" "$root/.claude/skills/shed"
	linked=" .claude/skills/shed"
	for agent in .cursor .codex; do
		if [ -d "$root/$agent" ]; then
			copy_skill "$src" "$root/$agent/skills/shed"
			linked="$linked $agent/skills/shed"
		fi
	done

	echo "Installed the shed skill into $root:$linked"
	echo "Commit .claude/skills/shed to keep the skill with the project."
}

if [ "$SCOPE" = "local" ]; then
	install_local
else
	install_global
fi
