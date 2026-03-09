#!/bin/bash

# Uninstall script for Git attribution hook
# This script removes the prepare-commit-msg hook from the current Git repository

set -e

# Check if we're in a Git repository
if [ ! -d ".git" ]; then
    echo "Error: This script must be run from the root of a Git repository"
    echo "Current directory: $(pwd)"
    exit 1
fi

HOOK_FILE=".git/hooks/prepare-commit-msg"

# Check if the hook exists
if [ ! -f "$HOOK_FILE" ]; then
    echo "Warning: Git hook not found at $HOOK_FILE"
    echo "Nothing to uninstall."
    exit 0
fi

# Verify it's our hook by checking if it contains CoreClaw attribution
if ! grep -q "CoreClaw attribution" "$HOOK_FILE"; then
    echo "Warning: The existing hook does not appear to be the CoreClaw attribution hook"
    echo "Please verify the hook file before removing it manually."
    exit 1
fi

# Remove the hook
rm "$HOOK_FILE"

echo "✓ Git hook removed successfully from $HOOK_FILE"
echo "✓ CoreClaw attribution will no longer be appended to your commit messages"
