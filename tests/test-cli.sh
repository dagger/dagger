#!/usr/bin/env bash
set -o errexit -o errtrace -o functrace -o nounset -o pipefail

# Test Directory
d=$(cd "$(dirname "${BASH_SOURCE[0]:-$PWD}")" 2>/dev/null 1>&2 && pwd)

test::cli() {
  local dagger="$1"

  test::cli::list "$dagger"
  test::cli::newdir "$dagger"
  test::cli::newgit "$dagger"
  test::cli::query "$dagger"
}

test::cli::list() {
  local dagger="$1"

  # Create temporary store
  local DAGGER_STORE
  DAGGER_STORE="$(mktemp -d -t dagger-store-XXXXXX)"
  export DAGGER_STORE

  test::one "CLI: list: no deployments" --stdout="" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" list

  test::one "CLI: list: create deployment" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" new --plan-dir "$d"/cli/simple simple

  test::one "CLI: list: with deployments" --stdout="simple" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" list
}

test::cli::newdir() {
  local dagger="$1"

  # Create temporary store
  local DAGGER_STORE
  DAGGER_STORE="$(mktemp -d -t dagger-store-XXXXXX)"
  export DAGGER_STORE

  test::one "CLI: new: --plan-dir" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" new --plan-dir "$d"/cli/simple simple

  test::one "CLI: new: duplicate name" --exit=1 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" new --plan-dir "$d"/cli/simple simple

  test::one "CLI: new: verify plan can be upped" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" up -d "simple"

  test::one "CLI: new: verify we have the right plan" --stdout='{
    foo: "value"
    bar: "another value"
}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" query -f cue -d "simple" -c
}

test::cli::newgit() {
  local dagger="$1"

  # Create temporary store
  local DAGGER_STORE
  DAGGER_STORE="$(mktemp -d -t dagger-store-XXXXXX)"
  export DAGGER_STORE

  test::one "CLI: new: --plan-git" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" new --plan-git https://github.com/samalba/dagger-test.git simple

  test::one "CLI: new: verify plan can be upped" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" up -d "simple"

  test::one "CLI: new: verify we have the right plan" --stdout='{
    foo: "value"
    bar: "another value"
}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" query -d "simple" -c
}

test::cli::query() {
  local dagger="$1"

  # Create temporary store
  local DAGGER_STORE
  DAGGER_STORE="$(mktemp -d -t dagger-store-XXXXXX)"
  export DAGGER_STORE

  test::one "CLI: query: initialize simple" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" new --plan-dir "$d"/cli/simple simple

  test::one "CLI: query: concrete" --stdout='{
    foo: "value"
    bar: "another value"
}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" query -f cue -d "simple" -c

  test::one "CLI: query: target" --stdout='"value"' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" query -f cue -d "simple" foo

  test::one "CLI: query: initialize nonconcrete" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" new --plan-dir "$d"/cli/nonconcrete nonconcrete

  test::one "CLI: query: non concrete" --exit=1 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" query -f cue -d "nonconcrete" -c
}
