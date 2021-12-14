setup() {
    load 'node_modules/bats-support/load'
    load 'node_modules/bats-assert/load'
}

@test "simple bats test" {
    run echo "Hello world"
    assert_success

    run cat /do/not/exist
    assert_failure
}
