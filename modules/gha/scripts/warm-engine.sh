#!/bin/bash

# Detect if a dev engine is available, if so: use that
# We don't rely on PATH because the GHA runner messes with that
if [[ -n "$_EXPERIMENTAL_DAGGER_CLI_BIN" ]]; then
  cli_bin_dir="$(dirname "$_EXPERIMENTAL_DAGGER_CLI_BIN")"
  export PATH="$cli_bin_dir:$PATH"
  unset _EXPERIMENTAL_DAGGER_RUNNER_HOST
fi

which dagger
dagger version
dagger core version
