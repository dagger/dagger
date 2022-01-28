setup() {
    load '../../bats_helpers'

    common_setup
}

@test "bash.#Run" {
    dagger up ./run-simple
}

