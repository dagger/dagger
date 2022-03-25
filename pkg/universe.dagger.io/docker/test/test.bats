setup() {
    load '../../bats_helpers'

    common_setup
}

@test "docker" {
    dagger "do" -p ./build.cue test
    dagger "do" -p ./dockerfile.cue test
    dagger "do" -p ./run.cue test
    dagger "do" -p ./image.cue test
}
