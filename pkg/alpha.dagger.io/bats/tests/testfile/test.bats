setup() {
    load 'mods/bats-support/load'
    load 'mods/bats-assert/load'
}

@test "simple bats test" {
    run echo "Hello world"
    assert_success

    run cat /do/not/exist
    assert_failure
}
