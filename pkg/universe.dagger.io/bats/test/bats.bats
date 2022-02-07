setup() {
    load '../../bats_helpers'

    common_setup
}

@test "bats.#Bats" {
    dagger up ./bats-test.cue
}

