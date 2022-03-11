setup() {
    load '../../bats_helpers'

    common_setup
}

@test "pwsh" {
    dagger "do" -p ./test.cue test
}

