setup() {
    load '../../../bats_helpers'

    common_setup
}

@test "aws/cli" {
    dagger up ./sts_get_caller_identity.cue
}
