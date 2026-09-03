#!/usr/bin/env node
"use strict";

const fs = require("fs");
const os = require("os");
const path = require("path");

function die(message) {
  console.error("shed-skills: " + message);
  process.exit(1);
}

function usage() {
  process.stdout.write(`Install the shed agent skill.

Usage: shed-skills [--global | --local] [--dir <path>]

  --global   This machine: copy into ~/.shed/skills and symlink into
             every coding agent found in $HOME (default)
  --local    This project: copy into .claude/skills/shed, and into
             .cursor/skills and .codex/skills when those directories exist
  --dir <p>  Project root for --local (default: the current directory)
  -h, --help Show this message

The same installer as:
  curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install-skills.sh | sh
`);
}

function skillSrc() {
  const packed = path.join(__dirname, "skill");
  const checkout = path.join(__dirname, "..", "..", "skills", "shed");
  if (fs.existsSync(path.join(packed, "SKILL.md"))) {
    return packed;
  }
  if (fs.existsSync(path.join(checkout, "SKILL.md"))) {
    return checkout;
  }
  die("skill files are missing from this package");
}

function copySkill(src, dest) {
  fs.rmSync(dest, { recursive: true, force: true });
  fs.mkdirSync(path.dirname(dest), { recursive: true });
  fs.cpSync(src, dest, { recursive: true });
}

function linkSkill(src, dest) {
  fs.mkdirSync(path.dirname(dest), { recursive: true });
  fs.rmSync(dest, { recursive: true, force: true });
  fs.symlinkSync(src, dest, "dir");
}

function parseArgs(argv) {
  let scope = "global";
  let dir = "";
  for (let i = 0; i < argv.length; i += 1) {
    switch (argv[i]) {
      case "--global":
        scope = "global";
        break;
      case "--local":
        scope = "local";
        break;
      case "--dir":
        i += 1;
        dir = argv[i];
        if (!dir) {
          die("--dir needs a path");
        }
        break;
      case "-h":
      case "--help":
        usage();
        process.exit(0);
        break;
      default:
        die("unknown option " + argv[i]);
    }
  }
  return { scope, dir };
}

function installGlobal(src) {
  const home = os.homedir();
  const stored = path.join(home, ".shed", "skills", "shed");
  copySkill(src, stored);

  const linked = [];
  const claude = path.join(home, ".claude");
  fs.mkdirSync(path.join(claude, "skills"), { recursive: true });
  for (const name of [".claude", ".cursor", ".codex"]) {
    const agentDir = path.join(home, name);
    if (!fs.existsSync(agentDir)) {
      continue;
    }
    linkSkill(stored, path.join(agentDir, "skills", "shed"));
    linked.push(name);
  }
  console.log("Installed the shed skill for: " + linked.join(" "));
  console.log("Rerun npx @shed-sh/skills any time to update it.");
}

function installLocal(src, projectDir) {
  const root = path.resolve(projectDir || ".");
  if (!fs.existsSync(root) || !fs.statSync(root).isDirectory()) {
    die("no directory named " + root);
  }

  copySkill(src, path.join(root, ".claude", "skills", "shed"));
  const linked = [".claude/skills/shed"];
  for (const name of [".cursor", ".codex"]) {
    if (fs.existsSync(path.join(root, name))) {
      copySkill(src, path.join(root, name, "skills", "shed"));
      linked.push(name + "/skills/shed");
    }
  }
  console.log("Installed the shed skill into " + root + ": " + linked.join(" "));
  console.log("Commit .claude/skills/shed to keep the skill with the project.");
}

const args = parseArgs(process.argv.slice(2));
if (args.dir && args.scope !== "local") {
  die("--dir is only valid with --local");
}
const src = skillSrc();
if (args.scope === "local") {
  installLocal(src, args.dir);
} else {
  installGlobal(src);
}
