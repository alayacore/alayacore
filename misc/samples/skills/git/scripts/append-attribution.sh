#!/bin/bash

# Git prepare-commit-msg hook to append CoreClaw attribution
# This script is automatically executed by Git before finalizing a commit message

COMMIT_MSG_FILE=$1
COMMIT_SOURCE=$2
SHA1=$3

# Get the directory where this script is located
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
ATTRIBUTION_FILE="$SCRIPT_DIR/../templates/attribution.txt"

# Only append attribution if this is a normal commit (not a merge, squash, etc.)
# and the attribution isn't already present
if [ "$COMMIT_SOURCE" = "message" ] || [ "$COMMIT_SOURCE" = "template" ] || [ "$COMMIT_SOURCE" = "merge" ] || [ -z "$COMMIT_SOURCE" ]; then
    # Check if attribution already exists in the commit message
    if ! grep -q "Generated with CoreClaw" "$COMMIT_MSG_FILE"; then
        # Append the attribution to the commit message
        cat "$ATTRIBUTION_FILE" >> "$COMMIT_MSG_FILE"
    fi
fi
