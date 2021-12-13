setup() {
    load 'helpers'

    common_setup
}

@test "task: #Pull" {
    cd "$TESTDIR"/tasks/pull
    dagger --europa up
}

@test "task: #ReadFile" {
    cd "$TESTDIR"/tasks/readfile
    dagger --europa up
}