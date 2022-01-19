setup() {
    load '../../bats_helpers'

    common_setup
}

@test "alpine.#Build" {
    "$DAGGER" up ./image-version.cue
    "$DAGGER" up ./package-install.cue
}