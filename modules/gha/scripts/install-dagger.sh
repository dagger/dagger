#!/bin/bash

set -o pipefail
# Fallback to /usr/local for backwards compatability
prefix_dir="${RUNNER_TEMP:-/usr/local}"

# Ensure the dir is writable otherwise fallback to tmpdir
if [[ ! -d "$prefix_dir" ]] || [[ ! -w "$prefix_dir" ]]; then
    prefix_dir="$(mktemp -d)"
fi
printf '%s/bin' "$prefix_dir" >> $GITHUB_PATH

# If the dagger version is 'latest', set the version back to an empty
# string. This allows the install script to detect and install the latest
# version itself
if [[ "$DAGGER_VERSION" == "latest" ]]; then
  DAGGER_VERSION=
fi

# The install.sh script creates path ${prefix_dir}/bin
curl -fsS https://dl.dagger.io/dagger/install.sh | BIN_DIR=${prefix_dir}/bin sh
