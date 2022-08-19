setup() {
    load '../../../bats_helpers'

    common_setup
}

@test "npm" {
    dagger "do" test
}
