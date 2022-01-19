setup() {
    load '../../bats_helpers'

    common_setup
}

@test "alpine.#Build" {
    dagger up ./image-version.cue
    dagger up ./package-install.cue
}