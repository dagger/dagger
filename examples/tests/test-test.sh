#!/usr/bin/env bash
set -o errexit -o errtrace -o functrace -o nounset -o pipefail

# The purpose of this is solely to test::one the test::one framework in test-lib.sh

readonly d=$(cd "$(dirname "${BASH_SOURCE[0]:-$PWD}")" 2>/dev/null 1>&2 && pwd)

# Performing self-diagnostic linting check first
shellcheck "$d/"*.sh

# shellcheck source=/dev/null
. "$d/test-lib.sh"

########################################################################
# Verifying the test::one framework is working
########################################################################
self::command(){
  local stdout="${1:-}"
  local stderr="${2:-}"
  local ret="${3:-0}"
  printf "%s" "$stdout"
  >&2 printf "%s" "$stderr"
  exit "$ret"
}

self::test(){
  # Command success testing
  test::one "Command success, no expectation should succeed" self::command || {
    logger::error "FAIL!"
    exit 1
  }

  test::one "Command success, --exit=0 should succeed" self::command --exit=0 || {
    logger::error "FAIL!"
    exit 1
  }

  test::one "Command success, --exit=1 should fail" self::command --exit=1 && {
    logger::error "FAIL!"
    exit 1
  }

  test::one "Command success, matching --stderr should succeed" self::command "" "to err" --stderr="to err" || {
    logger::error "FAIL!"
    exit 1
  }

  test::one "Command success, non matching --stderr should fail" self::command --stderr="to stderr" && {
    logger::error "FAIL!"
    exit 1
  }

  test::one "Command success, matching --stdout foo should succeed" self::command "lol foo" --stdout="lol foo" || {
    logger::error "FAIL!"
    exit 1
  }

  test::one "Command success, non matching --stdout should fail" self::command "lol foo" --stdout="lol" && {
    logger::error "FAIL!"
    exit 1
  }

  test::one "Command success, all expectation match should succeed" self::command "lol" --exit=0 --stdout="lol" --stderr= || {
    logger::error "FAIL!"
    exit 1
  }

  # Command failure testing
  test::one "Command failure, no expectation should fail" self::command "" "" 10 && {
    logger::error "FAIL!"
    exit 1
  }

  test::one "Command failure, --exit=0 should fail" self::command "" "" 10 --exit=0 && {
    logger::error "FAIL!"
    exit 1
  }

  test::one "Command failure, --exit=10 should succeed" self::command "" "" 10 --exit=10 || {
    logger::error "FAIL!"
    exit 1
  }

  test::one "Command failure, matching --stderr should succeed" self::command "" "" 10 --exit=10 --stderr= || {
    logger::error "FAIL!"
    exit 1
  }

  test::one "Command failure, non matching --stderr should fail" self::command "" "" 10 --exit=10 --stderr=lala && {
    logger::error "FAIL!"
    exit 1
  }

  test::one "Command failure, matching --stdout should succeed" self::command "to stdout" "" 10 --exit=10 --stdout="to stdout" || {
    logger::error "FAIL!"
    exit 1
  }

  test::one "Command failure, non matching --stdout should fail" self::command "to stdout" "" 10 --exit=10 --stdout="non matching" && {
    logger::error "FAIL!"
    exit 1
  }

  test::one "Command failure, all expectation match should succeed" self::command "to stdout" "to stderr" 10 --exit=10  --stdout="to stdout" --stderr="to stderr" || {
    logger::error "FAIL!"
    exit 1
  }
}

>&2 logger::info "Performing self-diagnostic"
self::test
>&2 logger::info "All tests successful. Test framework is operational."
