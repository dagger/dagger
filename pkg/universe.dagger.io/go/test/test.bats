setup() {
    load '../../bats_helpers'

    common_setup
}

@test "bash" {
    dagger up ./build.cue
    dagger up ./container.cue
    dagger up ./image.cue
    dagger up ./test.cue
}
