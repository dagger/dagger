#!/usr/bin/env bash
set -o errexit -o errtrace -o functrace -o nounset -o pipefail

# Test Directory
d=$(cd "$(dirname "${BASH_SOURCE[0]:-$PWD}")" 2>/dev/null 1>&2 && pwd)

test::examples() {
  local dagger="$1"

  test::secret "$d"/examples/react/inputs.yaml "examples: React" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/../examples/react
}
