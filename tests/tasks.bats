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
    cd "$TESTDIR"/tasks/write_file
    dagger --europa up ./write_file.cue
}

@test "task: #WriteFile failure: different contents" {
    cd "$TESTDIR"/tasks/write_file
    run dagger --europa up ./write_file_failure_diff_contents.cue
    assert_failure 
}