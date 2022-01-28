setup() {
    load 'helpers'

    common_setup
}

@test "task: #Pull" {
    cd "$TESTDIR"/tasks/pull
    "$DAGGER" --europa up ./pull.cue
}

@test "task: #Pull with auth" {
    cd "$TESTDIR"
    "$DAGGER" --europa up ./tasks/pull/pull_auth.cue
}

@test "task: #Push" {
    cd "$TESTDIR"
    "$DAGGER" --europa up ./tasks/push/push.cue
}

@test "task: #ReadFile" {
    cd "$TESTDIR"/tasks/readfile
    "$DAGGER" --europa up
}

@test "task: #WriteFile" {
    cd "$TESTDIR"/tasks/writefile
    "$DAGGER" --europa up ./writefile.cue
}

@test "task: #WriteFile failure: different contents" {
    cd "$TESTDIR"/tasks/writefile
    run "$DAGGER" --europa up ./writefile_failure_diff_contents.cue
    assert_failure 
}

@test "task: #Exec" {
    cd "$TESTDIR"/tasks/exec
    "$DAGGER" --europa up ./args.cue
    "$DAGGER" --europa up ./env.cue
    "$DAGGER" --europa up ./hosts.cue

    "$DAGGER" --europa up ./mount_cache.cue
    "$DAGGER" --europa up ./mount_fs.cue
    TESTSECRET="hello world" "$DAGGER" --europa up ./mount_secret.cue
    "$DAGGER" --europa up ./mount_tmp.cue
    "$DAGGER" --europa up ./mount_service.cue

    "$DAGGER" --europa up ./user.cue
    "$DAGGER" --europa up ./workdir.cue
}

@test "task: #Copy" {
    cd "$TESTDIR"/tasks/copy
    "$DAGGER" --europa up ./copy_exec.cue
    "$DAGGER" --europa up ./copy_file.cue

    run "$DAGGER" --europa up ./copy_exec_invalid.cue
    assert_failure
}

@test "task: #Mkdir" {
    # Make directory
    cd "$TESTDIR"/tasks/mkdir
    "$DAGGER" --europa up ./mkdir.cue

    # Create parents
    cd "$TESTDIR"/tasks/mkdir
    "$DAGGER" --europa up ./mkdir_parents.cue

    # Disable parents creation
    cd "$TESTDIR"/tasks/mkdir
    run "$DAGGER" --europa up ./mkdir_failure_disable_parents.cue
    assert_failure
}

@test "task: #Dockerfile" {
    cd "$TESTDIR"/tasks/dockerfile

    "$DAGGER" --europa up ./dockerfile.cue
    "$DAGGER" --europa up ./inlined_dockerfile.cue
    "$DAGGER" --europa up ./inlined_dockerfile_heredoc.cue
    "$DAGGER" --europa up ./dockerfile_path.cue
    "$DAGGER" --europa up ./build_args.cue
    "$DAGGER" --europa up ./image_config.cue
    "$DAGGER" --europa up ./labels.cue
    "$DAGGER" --europa up ./platform.cue
    "$DAGGER" --europa up ./build_auth.cue
}
@test "task: #Scratch" {
    cd "$TESTDIR"/tasks/scratch
    "$DAGGER" --europa up ./scratch.cue -l debug
    "$DAGGER" --europa up ./scratch_build_scratch.cue -l debug
    "$DAGGER" --europa up ./scratch_writefile.cue -l debug
}

@test "task: #Subdir" {
    cd "$TESTDIR"/tasks/subdir
    "$DAGGER" --europa up ./subdir_simple.cue

    run "$DAGGER" --europa up ./subdir_invalid_path.cue
    assert_failure
    
    run "$DAGGER" --europa up ./subdir_invalid_exec.cue
    assert_failure
}

@test "task: #GitPull" {
    cd "$TESTDIR"
    "$DAGGER" --europa up ./tasks/gitpull/exists.cue
    "$DAGGER" --europa up ./tasks/gitpull/git_dir.cue
    "$DAGGER" --europa up ./tasks/gitpull/private_repo.cue

    run "$DAGGER" --europa up ./tasks/gitpull/invalid.cue
    assert_failure
    run "$DAGGER" --europa up ./tasks/gitpull/bad_remote.cue
    assert_failure
    run "$DAGGER" --europa up ./tasks/gitpull/bad_ref.cue
    assert_failure
}

@test "task: #HTTPFetch" {
    cd "$TESTDIR"
    "$DAGGER" --europa up ./tasks/httpfetch/exist.cue
    run "$DAGGER" --europa up ./tasks/httpfetch/not_exist.cue
    assert_failure
}

@test "task: #NewSecret" {
    cd "$TESTDIR"/tasks/newsecret

    "$DAGGER" --europa up ./newsecret.cue
}

@test "task: #Source" {
    cd "$TESTDIR"/tasks/source
    "$DAGGER" --europa up ./source.cue
    "$DAGGER" --europa up ./source_include_exclude.cue
    "$DAGGER" --europa up ./source_relative.cue

    run "$DAGGER" --europa up ./source_invalid_path.cue
    assert_failure

    run "$DAGGER" --europa up ./source_not_exist.cue
    assert_failure
}
