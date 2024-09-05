#!/bin/bash

git symbolic-ref HEAD
exit 0

# Check if HEAD is detached (points directly to a commit hash)
if git rev-parse --verify HEAD >/dev/null 2>&1; then
    # Check if it's a tag
    TAG=$(git describe --tags --exact-match 2>/dev/null)
    if [ -n "$TAG" ]; then
        echo -n "refs/tags/$TAG"
    else
        # If not a tag, check if it's a branch
        BRANCH=$(git symbolic-ref -q HEAD 2>/dev/null)
        if [ -n "$BRANCH" ]; then
            echo -n "$BRANCH"
        else
            # Detached HEAD state, return the commit hash
            echo -n "$(git rev-parse HEAD)"
        fi
    fi
else
    echo "Error: Unable to resolve Git ref."
    exit 1
fi
