#!/usr/bin/env bash
set -o errexit -o errtrace -o functrace -o nounset -o pipefail

# Test Directory
d=$(cd "$(dirname "${BASH_SOURCE[0]:-$PWD}")" 2>/dev/null 1>&2 && pwd)

test::stdlib(){
  local dagger="$1"

  test::one "stdlib: alpine" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/alpine
  test::one "stdlib: react" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/js/react --input-dir TestData="$d"/stdlib/js/react/testdata
  test::one "stdlib: go" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/go --input-dir TestData="$d"/stdlib/go/testdata
  test::one "stdlib: file" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/file
  test::secret "$d"/stdlib/netlify/inputs.yaml "stdlib: netlify" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/netlify
}
