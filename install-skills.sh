#!/bin/sh
# Install the shed agent skill.
#
#   curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install-skills.sh | sh
#
# The skill lives in one place — a git clone under ~/.shed — and every coding
# agent gets a symlink into it. Re-running this script updates the clone, and
# every agent sees the update at once because they all point at the same copy.
# git is the only requirement; no Node, no package manager.
set -eu

REPO_URL="${SHED_SKILLS_REPO:-https://github.com/shed-sh/cli}"
SHED_HOME="${SHED_HOME:-$HOME/.shed}"
CLONE_DIR="$SHED_HOME/skills"

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
	cat <<'EOF'
Install the shed agent skill for every coding agent on this machine.

Usage: install-skills.sh

Clones the skill into ~/.shed/skills (or updates the existing clone) and
symlinks it into each agent's skill directory. Takes no options.

Environment: SHED_HOME (default ~/.shed)
EOF
	exit 0
fi

die() {
	echo "install-skills.sh: $*" >&2
	exit 1
}

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

[ -d "$CLONE_DIR/skills/shed" ] || die "$CLONE_DIR does not contain skills/shed"

# One copy, symlinked into every agent that is present on this machine.
# The Claude Code directory is always created: it is the shared Agent Skills
# location and creating it does no harm when the agent arrives later.
linked=""
mkdir -p "$HOME/.claude/skills"
for agent_dir in "$HOME/.claude" "$HOME/.cursor" "$HOME/.codex"; do
	[ -d "$agent_dir" ] || continue
	mkdir -p "$agent_dir/skills"
	ln -sfn "$CLONE_DIR/skills/shed" "$agent_dir/skills/shed"
	linked="$linked ${agent_dir#"$HOME"/}"
done

echo "Installed the shed skill for:$linked"
echo "Rerun this script any time to update it."
