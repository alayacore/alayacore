#!/bin/bash

# Install script for Git attribution hook
# This script installs the prepare-commit-msg hook in the current Git repository

set -e

# Get the directory where this script is located
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
SKILL_DIR="$( cd "$SCRIPT_DIR/.." && pwd )"

# Check if we're in a Git repository
if [ ! -d ".git" ]; then
    echo "Error: This script must be run from the root of a Git repository"
    echo "Current directory: $(pwd)"
    exit 1
fi

# Create .git/hooks directory if it doesn't exist
mkdir -p .git/hooks

# Define paths
HOOK_FILE=".git/hooks/prepare-commit-msg"
ATTRIBUTION_FILE="$SKILL_DIR/templates/attribution.txt"

# Copy the hook script to .git/hooks/prepare-commit-msg
cp "$SCRIPT_DIR/append-attribution.sh" "$HOOK_FILE"

# Replace the relative path with absolute path to attribution file
sed -i "s|ATTRIBUTION_FILE=.*|ATTRIBUTION_FILE=\"$ATTRIBUTION_FILE\"|" "$HOOK_FILE"

# Make the hook executable
chmod +x "$HOOK_FILE"

echo "✓ Git hook installed successfully at $HOOK_FILE"
echo "✓ CoreClaw attribution will now be appended to your commit messages"
