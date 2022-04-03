setup() {
    load '../../../../bats_helpers'

    common_setup
}

@test "dotnet" {
    dagger "do" -p ./publish.cue test
    dagger "do" -p ./image.cue test
    dagger "do" -p ./test.cue test
}
