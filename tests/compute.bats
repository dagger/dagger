setup() {
    load 'helpers'

    common_setup
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

    # Make sure the secret doesn't show in dagger query
    run "$DAGGER" query mySecret.id -f text
    assert_success
    assert_output "secret=mySecret"
}

@test "compute: exclude" {
    "$DAGGER" up -w "$TESTDIR"/compute/exclude
}
