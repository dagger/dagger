setup() {
    load '../../bats_helpers'

    common_setup
}

@test "bash" {
    dagger up
}

