#!/usr/bin/env bash
set -o errexit -o errtrace -o functrace -o nounset -o pipefail

# Source the lib
readonly d=$(cd "$(dirname "${BASH_SOURCE[0]:-$PWD}")" 2>/dev/null 1>&2 && pwd)
# shellcheck source=/dev/null
. "$d/test-lib.sh"

# Point this to your dagger binary
readonly DAGGER_BINARY="${DAGGER_BINARY:-$d/../../cmd/dagger/dagger}"
# The default arguments are a no-op, but having "anything" is a little cheat necessary for "${DAGGER_BINARY_ARGS[@]}" to not be empty down there
DAGGER_BINARY_ARGS="${DAGGER_BINARY_ARGS:---log-format json}"
read -ra DAGGER_BINARY_ARGS <<< "${DAGGER_BINARY_ARGS:-}"
readonly DAGGER_BINARY_ARGS

test::compute(){
  local dagger="$1"

  # Compute
  test::one "Compute: invalid string should fail" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/invalid/string
  test::one "Compute: invalid bool should fail" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/invalid/bool
  test::one "Compute: invalid int should fail" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/invalid/int
  test::one "Compute: invalid struct should fail" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/invalid/struct
  test::one "Compute: noop should succeed" --exit=0 --stdout='{"empty":{},"realempty":{}}'  \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/noop
  test::one "Compute: simple should succeed" --exit=0 --stdout="{}" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/simple
  test::one "Compute: script with undefined values should not fail" --exit=0 --stdout='{"hello":"world"}'  \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/undefined_prop
}

test::fetchcontainer(){
  local dagger="$1"

  # Fetch container
  test::one "FetchContainer: missing ref" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/fetch-container/invalid
  test::one "FetchContainer: non existent container image" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/fetch-container/nonexistent/image
  test::one "FetchContainer: non existent container tag" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/fetch-container/nonexistent/tag
  test::one "FetchContainer: non existent container digest" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/fetch-container/nonexistent/digest

  test::one "FetchContainer: valid containers"       --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/fetch-container/exist

  disable test::one "FetchContainer: non existent container image with valid digest (FIXME https://github.com/blocklayerhq/dagger/issues/32)" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/fetch-container/nonexistent/image-with-valid-digest
}

test::fetchgit(){
  local dagger="$1"

  # Fetch git
  test::one "FetchGit: valid" --exit=0 --stdout="{}" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/fetch-git/exist
  test::one "FetchGit: invalid" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/fetch-git/invalid
  test::one "FetchGit: non existent remote" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/fetch-git/nonexistent/remote
  test::one "FetchGit: non existent ref" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/fetch-git/nonexistent/ref
  test::one "FetchGit: non existent bork" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/fetch-git/nonexistent/bork
}

test::exec(){
  # Exec
  test::one "Exec: invalid" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/exec/invalid
  test::one "Exec: error" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/exec/error
  test::one "Exec: simple" --exit=0 --stdout={} \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/exec/simple
  # XXX should run twice and test that the string "always output" is visible with DOCKER_OUTPUT=1
  # Alternatively, use export, but this would test multiple things then...
  test::one "Exec: always" --exit=0 --stdout={} \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/exec/always
  test::one "Exec: env invalid" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/exec/env/invalid
  test::one "Exec: env valid" --exit=0 --stdout={} \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute  "$d"/exec/env/valid
  test::one "Exec: env with overlay" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute --input 'bar: "overlay environment"' "$d"/exec/env/overlay

  disable test::one "Exec: non existent dir (FIXME https://github.com/blocklayerhq/dagger/issues/30)" --exit=0 --stdout={} \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute  "$d"/exec/dir/doesnotexist
  disable test::one "Exec: valid dir (FIXME https://github.com/blocklayerhq/dagger/issues/30)" --exit=0 --stdout={} \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute  "$d"/exec/dir/exist
}

test::export(){
  test::one "Export: json" --exit=0 --stdout='{"test":{"something":"something"}}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/export/json

  test::one "Export: string" --exit=0 --stdout='{"test":"something"}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/export/string

  test::one "Export: string with additional constraint success" --exit=0 --stdout='{"test":"something"}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/export/withvalidation

  test::one "Export: many concurrent" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/export/concurrency

  test::one "Export: does not pass additional validation" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/export/invalid/validation

  test::one "Export: invalid format" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/export/invalid/format

  test::one "Export: invalid path" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/export/invalid/path

  disable test::one "Export: number (FIXME https://github.com/blocklayerhq/dagger/issues/36)" --exit=0 --stdout='{"test": -123.5}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/export/number
  disable test::one "Export: yaml (FIXME https://github.com/blocklayerhq/dagger/issues/36)" --exit=0 --stdout='XXXXXX' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/export/yaml
  disable test::one "Export: bool (FIXME https://github.com/blocklayerhq/dagger/issues/35)" --exit=0 --stdout='{"test": false}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/export/bool
}

test::copy(){
  test::one "Copy: valid components" --exit=0 --stdout='{"component":{},"test1":"lol","test2":"lol"}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/copy/valid/component
  test::one "Copy: valid script" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/copy/valid/script

  disable test::one "Copy: invalid caching (FIXME https://github.com/blocklayerhq/dagger/issues/44)" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/copy/invalid/cache
}

test::load(){
  test::one "Load: valid components" --exit=0 --stdout='{"component":{},"test1":"lol","test2":"lol"}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/load/valid/component
  test::one "Load: valid script" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/load/valid/script

  test::one "Load: invalid caching (FIXME https://github.com/blocklayerhq/dagger/issues/44)" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/load/invalid/cache
}

test::local(){
  disable "" "There are no local tests right now (the feature is possibly not functioning at all: see https://github.com/blocklayerhq/dagger/issues/41)"
}


test::mount(){
  disable test::one "Mount: tmpfs (FIXME https://github.com/blocklayerhq/dagger/issues/46)" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/mount/valid/tmpfs

  disable test::one "Mount: cache (FIXME https://github.com/blocklayerhq/dagger/issues/46)" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/mount/valid/cache

  disable test::one "Mount: component (FIXME https://github.com/blocklayerhq/dagger/issues/46)" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/mount/valid/component

  disable test::one "Mount: script (FIXME https://github.com/blocklayerhq/dagger/issues/46)" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/mount/valid/script
}

test::all(){
  local dagger="$1"

  test::load "$dagger"
  test::mount "$dagger"

  test::copy "$dagger"
  test::local "$dagger"
  test::compute "$dagger"
  test::fetchcontainer "$dagger"
  test::fetchgit "$dagger"
  test::exec "$dagger"
  test::export "$dagger"
}

case "${1:-all}" in
  # Help
  --help)
    echo "Run all known tests:"
    echo "  ./test.sh"
    echo "Run a specific cue module with expectations (all flags are optional if you just expect the command to succeed with no output validation:"
    echo "  ./test.sh cuefolder --exit=1 --stderr=lala --stdout=foo"
    ;;
  # Run all tests
  "all")
    test::all "$DAGGER_BINARY"
    ;;
  # Anything else means a single / custom test
  *)
    test::one "on demand $1" "$DAGGER_BINARY" compute "$@"
    ;;
esac
