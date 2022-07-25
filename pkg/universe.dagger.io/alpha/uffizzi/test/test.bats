setup() {
    load '../../../bats_helpers'

    common_setup
}

@test "uffizzi" {
    dagger "do" test
}
