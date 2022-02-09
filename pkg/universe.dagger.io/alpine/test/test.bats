setup() {
    load '../../bats_helpers'

    common_setup
}
@test "alpine" {
    dagger up
}
