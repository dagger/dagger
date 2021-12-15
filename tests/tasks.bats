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

@test "task: #WriteFile" {
    cd "$TESTDIR"/tasks/writefile
    dagger --europa up ./writefile.cue
}

@test "task: #WriteFile failure: different contents" {
    cd "$TESTDIR"/tasks/writefile
    run dagger --europa up ./writefile_failure_diff_contents.cue
    assert_failure 
}