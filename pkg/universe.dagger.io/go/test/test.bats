setup() {
    load '../../bats_helpers'

    common_setup
}

@test "go" {
    dagger "do" -p ./build.cue test
    dagger "do" -p ./container.cue test
    dagger "do" -p ./image.cue test
    dagger "do" -p ./test.cue test
}
