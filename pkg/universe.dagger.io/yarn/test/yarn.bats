setup() {
    load '../../bats_helpers'

    common_setup
}

@test "yarn.#Build (simple)" {
    dagger up ./simple
}
