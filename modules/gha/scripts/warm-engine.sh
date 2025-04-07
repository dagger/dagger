#!/bin/bash

# Detect if a dev engine is available, if so: use that
# We don't rely on PATH because the GHA runner messes with that
if [[ -n "$_EXPERIMENTAL_DAGGER_CLI_BIN" ]]; then
    ls -lh $(dirname $_EXPERIMENTAL_DAGGER_CLI_BIN)
    export PATH=$(dirname "$_EXPERIMENTAL_DAGGER_CLI_BIN"):$PATH
fi
if [[ -n "$USE_DEV_ENGINE" ]]; then
  # use runner host baked into the cli for dev jobs
  unset _EXPERIMENTAL_DAGGER_RUNNER_HOST
fi

# Run a simple query to "warm up" the engine
dagger version
dagger core version
