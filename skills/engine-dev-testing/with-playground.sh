#!/bin/sh

if [ -n "$DAGGER_CLOUD_ENGINE" ] && [ "$DAGGER_CLOUD_ENGINE" != "0" ] && [ "$DAGGER_CLOUD_ENGINE" != "false" ] && [ -z "$DAGGER_MODULE" ]; then
  echo "WARNING: this will run remotely in dagger cloud. If you have slow internet, consider setting DAGGER_MODULE to a remote git branch, to skip local file uploads. But remember to git push first!"
  echo "example: 'DAGGER_MODULE=github.com/dagger/dagger@upstream-branch $0 ...'"
fi

set -x

# Build the dagger engine playground, using the installed system dagger,
# with pre-downloaded sample source code for convenience,
# then execute the given inner command and print the output
dagger --progress=plain call \
  engine-dev \
  playground \
  with-directory --path=src/dagger --source=https://github.com/dagger/dagger#main \
  with-directory --path=src/demo-react-app --source=https://github.com/kpenfound/demo-react-app#main \
  with-exec --args=sh --args=-c --args="$*" \
  combined-output
