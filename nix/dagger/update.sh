#!/bin/bash

set -euxo pipefail

SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )

version=$1

function get_hash() {
  nix hash to-sri --type sha256 $(nix-prefetch-url https://github.com/dagger/dagger/releases/download/v${version}/dagger_v${version}_${1}.tar.gz)
}

current=$(cat <<EOF
{
  version = "${version}";
  hashes = {
    x86_64-linux = "$(get_hash linux_amd64)";
    x86_64-darwin = "$(get_hash darwin_amd64)";
    aarch64-linux = "$(get_hash linux_arm64)";
    aarch64-darwin = "$(get_hash darwin_arm64)";
  };
}
EOF)

echo "$current" > $SCRIPT_DIR/current.nix
