setup() {
    load '../../../../bats_helpers'

    common_setup
}

@test "rust" {
    dagger "do" -p ./publish.cue test
    dagger "do" -p ./image.cue test
}
