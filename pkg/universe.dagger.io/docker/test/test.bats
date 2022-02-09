setup() {
    load '../../bats_helpers'

    common_setup
}

@test "docker" {
    dagger up
}
