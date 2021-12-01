# Test core Dagger features & types

setup() {
    load 'helpers'

    common_setup
}

# This file combines 2 types of tests:
#  old-style tests: use 'dagger compute'
#  new-style tests: use 'dagger up'
#
# For new tests, please adopt new-style.
# NOTE: you will need to 'unset DAGGER_PROJECT'
# at the beginning of each new-style test.

@test "core: inputs & outputs" {
    dagger init

    dagger_new_with_plan test-core "$TESTDIR"/core/inputs-outputs

    # List available inputs
    run dagger -e test-core input list --show-optional
    assert_success
    assert_output --partial 'name'
    assert_output --partial 'dir'

    # Set dir input
    dagger -e test-core input dir dir "$DAGGER_PROJECT"

    # Set text input
    dagger -e test-core input text name Bob
    run dagger -e test-core up
    assert_success
    assert_output --partial 'Hello, Bob!'

    run dagger -e test-core output list
    assert_success
    assert_output --partial 'message  "Hello, Bob!"'

    # Unset text input
    dagger -e test-core input unset name
    run dagger -e test-core up
    assert_success
    assert_output --partial 'Hello, world!'
}


@test "compute: simple" {
    run "$DAGGER" compute "$TESTDIR"/compute/invalid/string
    assert_failure

    run "$DAGGER" compute "$TESTDIR"/compute/invalid/bool
    assert_failure

    run "$DAGGER" compute "$TESTDIR"/compute/invalid/int
    assert_failure

    run "$DAGGER" compute "$TESTDIR"/compute/invalid/struct
    assert_failure

    run "$DAGGER" compute "$TESTDIR"/compute/success/noop
    assert_success
    assert_line '{"empty":{}}'

    run "$DAGGER" compute "$TESTDIR"/compute/success/simple
    assert_success
    assert_line '{}'

    run "$DAGGER" compute "$TESTDIR"/compute/success/overload/flat
    assert_success

    run "$DAGGER" compute "$TESTDIR"/compute/success/overload/wrapped
    assert_success

    run "$DAGGER" compute "$TESTDIR"/compute/success/exec-nocache
    assert_success
}

@test "compute: dependencies" {
    run "$DAGGER" compute "$TESTDIR"/compute/dependencies/simple
    assert_success
    assert_line '{"A":{"result":"from A"},"B":{"result":"dependency from A"}}'

    run "$DAGGER" compute "$TESTDIR"/compute/dependencies/interpolation
    assert_success
    assert_line '{"A":{"result":"from A"},"B":{"result":"dependency from A"}}'

    run "$DAGGER" compute "$TESTDIR"/compute/dependencies/unmarshal
    assert_success
    assert_line '{"A":"{\"hello\": \"world\"}\n","B":{"result":"unmarshalled.hello=world"},"unmarshalled":{"hello":"world"}}'
}

@test "compute: inputs" {
    run "$DAGGER" compute "$TESTDIR"/compute/input/simple
    assert_success
    assert_line '{}'

    run "$DAGGER" compute --input-string 'in=foobar' "$TESTDIR"/compute/input/simple
    assert_success
    assert_line '{"in":"foobar","test":"received: foobar"}'

    run "$DAGGER" compute "$TESTDIR"/compute/input/default
    assert_success
    assert_line '{"in":"default input","test":"received: default input"}'

    run "$DAGGER" compute --input-string 'in=foobar' "$TESTDIR"/compute/input/default
    assert_success
    assert_line '{"in":"foobar","test":"received: foobar"}'

    run "$DAGGER" compute --input-string=foobar "$TESTDIR"/compute/input/default
    assert_failure
    assert_output --partial 'failed to parse input: input-string'

    run "$DAGGER" compute --input-dir=foobar "$TESTDIR"/compute/input/default
    assert_failure
    assert_output --partial 'failed to parse input: input-dir'

    run "$DAGGER" compute --input-git=foobar "$TESTDIR"/compute/input/default
    assert_failure
    assert_output --partial 'failed to parse input: input-git'
}

@test "compute: secrets" {
    # secrets used as environment variables must fail
    run "$DAGGER" compute  "$TESTDIR"/compute/secrets/invalid/env
    assert_failure
    assert_line --partial "conflicting values"

    # strings passed as secrets must fail
    run "$DAGGER" compute  "$TESTDIR"/compute/secrets/invalid/string
    assert_failure

    # Setting a text input for a secret value should fail
    run "$DAGGER" compute --input-string 'mySecret=SecretValue' "$TESTDIR"/compute/secrets/simple
    assert_failure

    # Now test with an actual secret and make sure it works
    "$DAGGER" init
    dagger_new_with_plan secrets "$TESTDIR"/compute/secrets/simple
    "$DAGGER" input secret mySecret SecretValue
    run "$DAGGER" up
    assert_success
}

@test "core: stream" {
    dagger init

    dagger_new_with_plan test-stream "$TESTDIR"/core/stream

    # Set dir input
    "$DAGGER" input socket dockersocket /var/run/docker.sock

    "$DAGGER" up
}

@test "core: platform config" {
  dagger init

  # Test for amd64 platform
  dagger_new_with_plan test-amd "$TESTDIR"/core/platform-config "linux/amd64"

  # Set arch expected value
  "$DAGGER" -e test-amd input text targetArch "x86_64"

  # Up amd
  "$DAGGER" -e test-amd up --no-cache

  # Test for amd64 platform
  dagger_new_with_plan test-arm "$TESTDIR"/core/platform-config "linux/arm64"

  # Set arch expected value
  "$DAGGER" -e test-arm input text targetArch "aarch64"

  # Up arm
  "$DAGGER" -e test-arm up --no-cache
}

@test "compute: exclude" {
    "$DAGGER" up --project "$TESTDIR"/compute/exclude
}
