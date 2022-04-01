#!/usr/bin/env bash

#
# Run a yarn script
#

# Create $ENVFILE_NAME file if set
[ -n "$ENVFILE_NAME" ] && echo "$ENVFILE" > "$ENVFILE_NAME"

opts=( $(echo $YARN_ARGS) )
yarn --cwd "$YARN_CWD" run "$YARN_BUILD_SCRIPT" ${opts[@]}
if [ ! -z "${YARN_BUILD_DIRECTORY:-}" ]; then
	mv "$YARN_BUILD_DIRECTORY" /build
else
	mkdir /build
fi
