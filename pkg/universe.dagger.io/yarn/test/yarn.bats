setup() {
    load '../../bats_helpers'

    common_setup
}

@test "yarn.#Build" {
    dagger up ./yarn-test.cue
}
