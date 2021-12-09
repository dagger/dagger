setup() {
    load 'helpers'

    common_setup
}

@test "task: #Pull" {
    cd "$TESTDIR"/tasks/pull
    dagger --europa up
}