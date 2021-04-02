#!/usr/bin/env bash
set -o errexit -o errtrace -o functrace -o nounset -o pipefail

readonly SUITE="${SUITE:-"all"}"

# Point this to your dagger binary
readonly DAGGER_BINARY="${DAGGER_BINARY:-$d/../cmd/dagger/dagger}"
# The default arguments are a no-op, but having "anything" is a little cheat necessary for "${DAGGER_BINARY_ARGS[@]}" to not be empty down there
DAGGER_BINARY_ARGS="${DAGGER_BINARY_ARGS:---log-format json}"
read -ra DAGGER_BINARY_ARGS <<< "${DAGGER_BINARY_ARGS:-}"
readonly DAGGER_BINARY_ARGS

# Test Directory
d=$(cd "$(dirname "${BASH_SOURCE[0]:-$PWD}")" 2>/dev/null 1>&2 && pwd)
# Source the lib
# shellcheck source=/dev/null
. "$d/test-lib.sh"
# shellcheck source=/dev/null
. "$d/test-compute.sh"
# shellcheck source=/dev/null
. "$d/test-llb.sh"
# shellcheck source=/dev/null
. "$d/test-stdlib.sh"
# shellcheck source=/dev/null
. "$d/test-cli.sh"
# shellcheck source=/dev/null
. "$d/test-examples.sh"

test::all(){
  local dagger="$1"

  test::suite "compute" && test::compute "$dagger"
  test::suite "llb" && test::llb "$dagger"
  test::suite "stdlib" && test::stdlib "$dagger"
  test::suite "cli" && test::cli "$dagger"
  test::suite "examples" && test::examples "$dagger"
}

test::suite() {
    local name="$1"

  {
    # if SUITE is "-foo", run everything that's not "foo"
    [[ "$SUITE" = -* ]] && [[ "$SUITE" != "-${name}" ]]
  } || {
    # Run a specific suite, or match all of them if suite is "all"
    [ "${SUITE}" = "all" ] || [ "${SUITE}" = "${name}" ]
  } || false
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
