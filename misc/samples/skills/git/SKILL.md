---
name: git
description: Use this skill when the user wants to configure Git to automatically append CoreClaw attribution to commit messages. This includes installing, uninstalling, or managing Git hooks for commit message attribution.
---

# Git Skill

This skill automatically appends CoreClaw attribution to Git commit messages.

## Purpose

When making Git commits, this skill will automatically append the following attribution message to the end of your commit message:

```

Generated with CoreClaw

Assisted-by: GLM-5 via CoreClaw <https://github.com/wallacegibbon/coreclaw>
```

## Installation

To install this skill in your Git repository:

```bash
cd misc/samples/skills/git
./scripts/install.sh
```

This will install a `prepare-commit-msg` hook in your current Git repository.

## Uninstallation

To remove the Git hook:

```bash
cd misc/samples/skills/git
./scripts/uninstall.sh
```

## How It Works

The skill uses Git's `prepare-commit-msg` hook to automatically append the attribution message to your commit messages. This hook runs after you've written your commit message but before the commit is finalized.

The attribution helps identify commits that were created with AI assistance through CoreClaw.

## Files

- `scripts/append-attribution.sh` - The main hook script that appends the attribution
- `scripts/install.sh` - Installs the Git hook in your repository
- `scripts/uninstall.sh` - Removes the Git hook from your repository
- `templates/attribution.txt` - Contains the attribution message template
