#!/usr/bin/env bash
set -o errexit -o errtrace -o functrace -o nounset -o pipefail

# Test Directory
d=$(cd "$(dirname "${BASH_SOURCE[0]:-$PWD}")" 2>/dev/null 1>&2 && pwd)

test::compute(){
  local dagger="$1"

  test::compute::simple "$dagger"
  test::compute::dependencies "$dagger"
  test::compute::input "$dagger"
  test::compute::daggerignore "$dagger"
}

test::compute::simple(){
  local dagger="$1"

  # Compute: invalid syntax
  test::one "Compute: invalid string should fail" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/invalid/string
  test::one "Compute: invalid bool should fail" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/invalid/bool
  test::one "Compute: invalid int should fail" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/invalid/int
  test::one "Compute: invalid struct should fail" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/invalid/struct

  # Compute: success
  test::one "Compute: noop should succeed" --exit=0 --stdout='{"empty":{}}'  \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/success/noop
  test::one "Compute: simple should succeed" --exit=0 --stdout="{}" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/success/simple
  test::one "Compute: overloading #Component should work" --exit=0  \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/success/overload/flat
  test::one "Compute: overloading #Component should work" --exit=0  \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/success/overload/wrapped
}

test::compute::dependencies(){
  local dagger="$1"

  test::one "Dependencies: simple direct dependency" --exit=0 --stdout='{"A":{"result":"from A"},"B":{"result":"dependency from A"}}'  \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/dependencies/simple
  test::one "Dependencies: interpolation" --exit=0 --stdout='{"A":{"result":"from A"},"B":{"result":"dependency from A"}}'  \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/dependencies/interpolation
  test::one "Dependencies: json.Unmarshal" --exit=0 --stdout='{"A":"{\"hello\": \"world\"}\n","B":{"result":"unmarshalled.hello=world"},"unmarshalled":{"hello":"world"}}'  \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/dependencies/unmarshal
}

test::compute::input(){
  local dagger="$1"

  # Compute: `--input-*`
  test::one "Compute: Input: missing input should skip execution" --exit=0 --stdout='{}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/input/simple

  test::one "Compute: Input: simple input" --exit=0 --stdout='{"in":"foobar","test":"received: foobar"}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute --input-string 'in=foobar' "$d"/compute/input/simple

  test::one "Compute: Input: default values" --exit=0 --stdout='{"in":"default input","test":"received: default input"}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/compute/input/default

  test::one "Compute: Input: override default value" --exit=0 --stdout='{"in":"foobar","test":"received: foobar"}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute --input-string 'in=foobar' "$d"/compute/input/default
}

test::compute::daggerignore() {
  test::one "Dagger Ignore" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute --input-dir TestData="$d"/compute/ignore/testdata "$d"/compute/ignore
}
