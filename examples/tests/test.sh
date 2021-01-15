#!/usr/bin/env bash
set -o errexit -o errtrace -o functrace -o nounset -o pipefail

# Source the lib
readonly d=$(cd "$(dirname "${BASH_SOURCE[0]:-$PWD}")" 2>/dev/null 1>&2 && pwd)
# shellcheck source=/dev/null
. "$d/test-lib.sh"

# Point this to your dagger binary
readonly DAGGER_BINARY="${DAGGER_BINARY:-$d/../../cmd/dagger/dagger}"


test::compute(){
  local dagger="$1"

  # Compute
  test::one "Compute: invalid string should fail" --exit=1 --stdout= \
      "$dagger" compute "$d"/compute/invalid/string
  test::one "Compute: invalid bool should fail" --exit=1 --stdout= \
      "$dagger" compute "$d"/compute/invalid/bool
  test::one "Compute: invalid int should fail" --exit=1 --stdout= \
      "$dagger" compute "$d"/compute/invalid/int
  test::one "Compute: invalid struct should fail" --exit=1 --stdout= \
      "$dagger" compute "$d"/compute/invalid/struct
  test::one "Compute: noop should succeed" --exit=0 --stdout='{"empty":{},"realempty":{},"withprops":{}}'  \
      "$dagger" compute "$d"/compute/noop
  # XXX https://github.com/blocklayerhq/dagger/issues/28
  #test::one "Compute: unresolved should fail" --exit=1 --stdout=  \
  #    "$dagger" compute "$d"/compute/invalid/undefined_prop
  test::one "Compute: simple should succeed" --exit=0 --stdout="{}" \
      "$dagger" compute "$d"/compute/simple
}

test::fetchcontainer(){
  local dagger="$1"

  # Fetch container
  test::one "FetchContainer: missing ref" --exit=1 --stdout= \
      "$dagger" compute "$d"/fetch-container/invalid
  test::one "FetchContainer: non existent container image" --exit=1 --stdout= \
      "$dagger" compute "$d"/fetch-container/nonexistent/image
  test::one "FetchContainer: non existent container tag" --exit=1 --stdout= \
      "$dagger" compute "$d"/fetch-container/nonexistent/tag
  test::one "FetchContainer: non existent container digest" --exit=1 --stdout= \
      "$dagger" compute "$d"/fetch-container/nonexistent/digest
  # XXX https://github.com/blocklayerhq/dagger/issues/32 - this will fail once cached by buildkit
  # test::one "FetchContainer: non existent container image with valid digest" --exit=1 --stdout= \
  #    "$dagger" compute "$d"/fetch-container/nonexistent/image-with-valid-digest
  test::one "FetchContainer: valid containers"       --exit=0 \
      "$dagger" compute "$d"/fetch-container/exist
}

test::fetchgit(){
  local dagger="$1"

  # Fetch git
  test::one "FetchGit: valid" --exit=0 --stdout="{}" \
      "$dagger" compute "$d"/fetch-git/exist
  test::one "FetchGit: invalid" --exit=1 --stdout= \
      "$dagger" compute "$d"/fetch-git/invalid
  test::one "FetchGit: non existent remote" --exit=1 --stdout= \
      "$dagger" compute "$d"/fetch-git/nonexistent/remote
  test::one "FetchGit: non existent ref" --exit=1 --stdout= \
      "$dagger" compute "$d"/fetch-git/nonexistent/ref
  test::one "FetchGit: non existent bork" --exit=1 --stdout= \
      "$dagger" compute "$d"/fetch-git/nonexistent/bork
}

test::exec(){
  # Exec
  test::one "Exec: invalid" --exit=1 --stdout= \
      "$dagger" compute "$d"/exec/invalid
  test::one "Exec: error" --exit=1 --stdout= \
      "$dagger" compute "$d"/exec/error
  test::one "Exec: simple" --exit=0 --stdout={} \
      "$dagger" compute "$d"/exec/simple
  # XXX should run twice and test that the string "always output" is visible with DOCKER_OUTPUT=1
  # Alternatively, use export, but this would test multiple things then...
  test::one "Exec: always" --exit=0 --stdout={} \
      "$dagger" compute "$d"/exec/always
  test::one "Exec: env invalid" --exit=1 --stdout= \
      "$dagger" compute "$d"/exec/env/invalid
  test::one "Exec: env valid" --exit=0 --stdout={} \
      "$dagger" compute  "$d"/exec/env/valid
  # XXX overlays are not wired yet
  # test::one "Exec: env with overlay" --exit=0 --stdout={} \
  #    "$dagger" compute --input-string 'bar: "overlay environment"' "$d"/exec/env/overlay
  # XXX broken right now: https://github.com/blocklayerhq/dagger/issues/30
  #test::one "Exec: non existent dir" --exit=0 --stdout={} \
  #    "$dagger" compute  "$d"/exec/dir/doesnotexist
  #test::one "Exec: valid dir" --exit=0 --stdout={} \
  #    "$dagger" compute  "$d"/exec/dir/exist
  test::one "Exec: args" --exit=0 \
      "$dagger" compute "$d"/exec/args
}

test::all(){
  local dagger="$1"

  test::compute "$dagger"
  test::fetchcontainer "$dagger"
  test::fetchgit "$dagger"
  test::exec "$dagger"

  # TODO: exec mounts
  # TODO: copy
  # TODO: load
  # TODO: local
  # TODO: export
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
