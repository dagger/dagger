setup() {
    load '../../bats_helpers'

    common_setup
}

@test "netlify.#Deploy" {
    dagger up ./netlify-test.cue
}
