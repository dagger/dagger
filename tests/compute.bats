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
}

@test ".daggerignore" {
    "$DAGGER" compute --input-dir TestData="$TESTDIR"/compute/ignore/testdata "$TESTDIR"/compute/ignore
}
