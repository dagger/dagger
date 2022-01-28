#!/bin/bash

# Create $ENVFILE_NAME file if set
[ -n "$ENVFILE_NAME" ] && echo "$ENVFILE" > "$ENVFILE_NAME"

yarn --cwd "$YARN_CWD" install --production false

# FIXME: get ops from argv?
yarn --cwd "$YARN_CWD" run "$YARN_BUILD_SCRIPT" "$@"
mv "$YARN_BUILD_DIRECTORY" /build
