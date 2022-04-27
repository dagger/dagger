setup() {
    load '../../../../bats_helpers'

    common_setup
}

@test "kapp" {
    cue "eval"
    # Not doing a dagger test since kapp
    # relies on kubernetes which we don't
    # currently have in dagger CI.
    # To validate locally on my Mac,
    # I installed 'kapp' via homebrewrun, and
    # enabled Kubernetes in Docker Desktop.
    # Then uncomment the following test lines:
    #
    #dagger "do" test deploy
    #dagger "do" test verify
    #dagger "do" test ls
    #dagger "do" test inspect
    #dagger "do" test delete
}
