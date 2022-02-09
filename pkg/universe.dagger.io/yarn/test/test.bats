setup() {
    load '../../bats_helpers'

    common_setup
}

@test "yarn" {
    dagger up
}
