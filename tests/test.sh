#!/usr/bin/env bash
set -o errexit -o errtrace -o functrace -o nounset -o pipefail

# Source the lib
readonly d=$(cd "$(dirname "${BASH_SOURCE[0]:-$PWD}")" 2>/dev/null 1>&2 && pwd)
# shellcheck source=/dev/null
. "$d/test-lib.sh"

# Point this to your dagger binary
readonly DAGGER_BINARY="${DAGGER_BINARY:-$d/../cmd/dagger/dagger}"
# The default arguments are a no-op, but having "anything" is a little cheat necessary for "${DAGGER_BINARY_ARGS[@]}" to not be empty down there
DAGGER_BINARY_ARGS="${DAGGER_BINARY_ARGS:---log-format json}"
read -ra DAGGER_BINARY_ARGS <<< "${DAGGER_BINARY_ARGS:-}"
readonly DAGGER_BINARY_ARGS

test::all(){
  local dagger="$1"

  test::llb "$dagger"
  test::stdlib "$dagger"
  test::dependencies "$dagger"
  test::compute "$dagger"
  test::daggerignore "$dagger"
  test::cli "$dagger"
  test::examples "$dagger"
}

test::llb(){
  local dagger="$1"

  test::llb::load "$dagger"
  test::llb::mount "$dagger"
  test::llb::copy "$dagger"
  test::llb::local "$dagger"
  test::llb::fetchcontainer "$dagger"
  test::llb::pushcontainer "$dagger"
  test::llb::fetchgit "$dagger"
  test::llb::exec "$dagger"
  test::llb::export "$dagger"
  test::llb::subdir "$dagger"
  test::llb::dockerbuild "$dagger"
}

test::stdlib(){
  local dagger="$1"

  test::one "stdlib: alpine" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/alpine
  test::one "stdlib: yarn" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/yarn --input-dir TestData="$d"/stdlib/yarn/testdata
  test::one "stdlib: go" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/go --input-dir TestData="$d"/stdlib/go/testdata
  test::one "stdlib: file" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/file
  test::secret "$d"/stdlib/netlify/inputs.yaml "stdlib: netlify" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/netlify
}

test::dependencies(){
  local dagger="$1"

  test::one "Dependencies: simple direct dependency" --exit=0 --stdout='{"A":{"result":"from A"},"B":{"result":"dependency from A"}}'  \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/dependencies/simple
  test::one "Dependencies: interpolation" --exit=0 --stdout='{"A":{"result":"from A"},"B":{"result":"dependency from A"}}'  \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/dependencies/interpolation
  test::one "Dependencies: json.Unmarshal" --exit=0 --stdout='{"A":"{\"hello\": \"world\"}\n","B":{"result":"unmarshalled.hello=world"},"unmarshalled":{"hello":"world"}}'  \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/dependencies/unmarshal
}

test::compute(){
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

test::daggerignore() {
  test::one "Dagger Ignore" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute --input-dir TestData="$d"/ignore/testdata "$d"/ignore
}

test::cli() {
  local dagger="$1"

  test::cli::list "$dagger"
  test::cli::newdir "$dagger"
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
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" query -d "simple" -c

  test::one "CLI: query: target" --stdout='"value"' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" query -d "simple" foo

  test::one "CLI: query: initialize nonconcrete" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" new --plan-dir "$d"/cli/nonconcrete nonconcrete

  test::one "CLI: query: non concrete" --exit=1 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" query -d "nonconcrete" -c
}

test::examples() {
    test::secret "$d"/../examples/react-netlify/inputs.yaml "examples: React Netlify" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/../examples/react-netlify

}

test::llb::fetchcontainer(){
  local dagger="$1"

  # Fetch container
  disable test::one "FetchContainer: missing ref (FIXME: distinguish missing inputs from incorrect config)" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/fetch-container/invalid
  test::one "FetchContainer: non existent container image" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/fetch-container/nonexistent/image
  test::one "FetchContainer: non existent container tag" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/fetch-container/nonexistent/tag
  test::one "FetchContainer: non existent container digest" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/fetch-container/nonexistent/digest

  test::one "FetchContainer: valid containers"       --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/fetch-container/exist

  disable test::one "FetchContainer: non existent container image with valid digest (FIXME https://github.com/blocklayerhq/dagger/issues/32)" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/fetch-container/nonexistent/image-with-valid-digest
}

test::llb::pushcontainer(){
  local dagger="$1"

  test::secret "$d"/llb/push-container/inputs.yaml "PushContainer: simple"       --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/push-container
}

test::llb::fetchgit(){
  local dagger="$1"

  # Fetch git
  test::one "FetchGit: valid" --exit=0 --stdout="{}" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/fetch-git/exist
  disable test::one "FetchGit: invalid (FIXME: distinguish missing inputs from incorrect config) " --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/fetch-git/invalid
  test::one "FetchGit: non existent remote" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/fetch-git/nonexistent/remote
  test::one "FetchGit: non existent ref" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/fetch-git/nonexistent/ref
  test::one "FetchGit: non existent bork" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/fetch-git/nonexistent/bork
}

test::llb::exec(){
  # Exec
  test::one "Exec: invalid" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/exec/invalid
  test::one "Exec: error" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/exec/error
  test::one "Exec: simple" --exit=0 --stdout={} \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/exec/simple
  # XXX should run twice and test that the string "always output" is visible with DOCKER_OUTPUT=1
  # Alternatively, use export, but this would test multiple things then...
  test::one "Exec: always" --exit=0 --stdout={} \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/exec/always
  test::one "Exec: env invalid" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/exec/env/invalid
  test::one "Exec: env valid" --exit=0 --stdout={} \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute  "$d"/llb/exec/env/valid
  test::one "Exec: env with overlay" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute --input-string 'bar=overlay environment' "$d"/llb/exec/env/overlay

  test::one "Exec: non existent dir" --exit=0 --stdout={} \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute  "$d"/llb/exec/dir/doesnotexist
  test::one "Exec: valid dir" --exit=0 --stdout={} \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute  "$d"/llb/exec/dir/exist

  disable test::one "Exec: exit code propagation (FIXME https://github.com/blocklayerhq/dagger/issues/74)" --exit=123 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/exec/exit_code

  test::one "Exec: script with referenced non-concrete property should not be executed, and should succeed overall" --exit=0 --stdout='{"hello":"world"}'  \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/exec/undefined/non_concrete_referenced
  # NOTE: the exec is meant to fail - and we test that as a way to confirm it has been executed
  test::one "Exec: script with unreferenced undefined properties should be executed" --exit=1 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/exec/undefined/non_concrete_not_referenced
  test::one "Exec: package with optional def, not referenced, should be executed" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/exec/undefined/with_pkg_def
  test::one "Exec: script with optional prop, not referenced, should be executed" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/exec/undefined/with_pkg_optional
  disable test::one "Exec: script with non-optional prop, not referenced, should be executed (FIXME https://github.com/blocklayerhq/dagger/issues/70)" --exit=1 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/exec/undefined/with_pkg_mandatory
}

test::llb::export(){
  test::one "Export: json" --exit=0 --stdout='{"testMap":{"something":"something"},"testScalar":true}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/export/json

  test::one "Export: string" --exit=0 --stdout='{"test":"something"}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/export/string

  test::one "Export: string with additional constraint success" --exit=0 --stdout='{"test":"something"}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/export/withvalidation

  test::one "Export: many concurrent" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/export/concurrency

  test::one "Export: does not pass additional validation" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/export/invalid/validation

  test::one "Export: invalid format" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/export/invalid/format

  test::one "Export: invalid path" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/export/invalid/path

  test::one "Export: number" --exit=0 --stdout='{"test":-123.5}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/export/float

  disable test::one "Export: number (FIXME: https://github.com/blocklayerhq/dagger/issues/96)" --exit=0 --stdout='{"test":-123.5}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/export/number

  test::one "Export: yaml" --exit=0 --stdout='{"testMap":{"something":"something"},"testScalar":true}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/export/yaml

  test::one "Export: bool" --exit=0 --stdout='{"test":true}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/export/bool
}

test::llb::copy(){
  test::one "Copy: valid components" --exit=0 --stdout='{"component":{},"test1":"lol","test2":"lol"}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/copy/valid/component
  test::one "Copy: valid script" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/copy/valid/script

  disable test::one "Copy: invalid caching (FIXME https://github.com/blocklayerhq/dagger/issues/44)" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/copy/invalid/cache
}

test::llb::load(){
  test::one "Load: valid components" --exit=0 --stdout='{"component":{},"test1":"lol","test2":"lol"}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/load/valid/component
  test::one "Load: valid script" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/load/valid/script

  test::one "Load: invalid caching (FIXME https://github.com/blocklayerhq/dagger/issues/44)" --exit=1 --stdout= \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/load/invalid/cache
}

test::llb::local(){
  disable "" "There are no local tests right now (the feature is possibly not functioning at all: see https://github.com/blocklayerhq/dagger/issues/41)"
}

test::llb::mount(){
  test::one "Mount: tmpfs" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/mounts/valid/tmpfs

  test::one "Mount: cache" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/mounts/valid/cache

  test::one "Mount: component" --exit=0  --stdout='{"test":"hello world"}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/mounts/valid/component

  disable test::one "Mount: script (FIXME https://github.com/blocklayerhq/dagger/issues/46)" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/mounts/valid/script
}

test::llb::subdir() {
  test::one "Subdir: simple usage" --exit=0 --stdout='{"hello":"world"}' \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/llb/subdir/simple
}

test::llb::dockerbuild() {
  test::one "Docker Build" --exit=0 \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute --input-dir TestData="$d"/llb/dockerbuild/testdata "$d"/llb/dockerbuild
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
