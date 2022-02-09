setup() {
    load '../../bats_helpers'

    common_setup
}

@test "netlify" {
    dagger up
}
