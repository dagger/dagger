setup() {
    load 'helpers'

    common_setup
    cd "$TESTDIR" || exit
}

@test "task: #Pull" {
    "$DAGGER" "do" -p ./tasks/pull/pull.cue pull
    "$DAGGER" "do" -p ./tasks/pull/pull_auth.cue pull
}

@test "task: #Push" {
    "$DAGGER" "do" -p ./tasks/push/push.cue pullContent
}

@test "task: #ReadFile" {
    "$DAGGER" "do" -p ./tasks/readfile/readfile.cue readfile
}

@test "task: #WriteFile" {
    "$DAGGER" "do" -p ./tasks/writefile/writefile.cue readfile
    run "$DAGGER" "do" -p ./tasks/writefile/writefile_failure_diff_contents.cue readfile
    assert_failure
}

@test "task: #Exec" {
    cd ./tasks/exec
    "$DAGGER" "do" -p ./args.cue verify
    "$DAGGER" "do" -p ./env.cue verify
    "$DAGGER" "do" -p ./env_secret.cue verify
    "$DAGGER" "do" -p ./hosts.cue verify

    "$DAGGER" "do" -p ./mount_cache.cue test
    "$DAGGER" "do" -p ./mount_fs.cue test
    TESTSECRET="hello world" "$DAGGER" "do" -p ./mount_secret.cue test
    "$DAGGER" "do" -p ./mount_tmp.cue verify
    "$DAGGER" "do" -p ./mount_service.cue verify

    "$DAGGER" "do" -p ./user.cue test
    "$DAGGER" "do" -p ./workdir.cue verify
}

@test "task: #Copy" {
    "$DAGGER" "do" -p ./tasks/copy/copy_exec.cue test
    "$DAGGER" "do" -p ./tasks/copy/copy_file.cue test

    run "$DAGGER" "do" -p ./tasks/copy/copy_exec_invalid.cue test
    assert_failure
}

@test "task: #Mkdir" {
    # Make directory
    "$DAGGER" "do" -p ./tasks/mkdir/mkdir.cue readChecker

    # Create parents
    "$DAGGER" "do" -p ./tasks/mkdir/mkdir_parents.cue readChecker

    # Disable parents creation
    run "$DAGGER" "do" -p ./tasks/mkdir/mkdir_failure_disable_parents.cue readChecker
    assert_failure
}

@test "task: #Dockerfile" {
    cd "$TESTDIR"/tasks/dockerfile
    "$DAGGER" "do" -p ./dockerfile.cue verify
    "$DAGGER" "do" -p ./inlined_dockerfile.cue verify
    "$DAGGER" "do" -p ./inlined_dockerfile_heredoc.cue verify
    "$DAGGER" "do" -p ./dockerfile_path.cue verify
    "$DAGGER" "do" -p ./build_args.cue build
    "$DAGGER" "do" -p ./image_config.cue build
    "$DAGGER" "do" -p ./labels.cue build
    "$DAGGER" "do" -p ./platform.cue build
    "$DAGGER" "do" -p ./build_auth.cue build
}

@test "task: #Scratch" {
    "$DAGGER" "do" -p ./tasks/scratch/scratch.cue exec
    "$DAGGER" "do" -p ./tasks/scratch/scratch_build_scratch.cue build
    "$DAGGER" "do" -p ./tasks/scratch/scratch_writefile.cue readfile
}

@test "task: #Subdir" {
    "$DAGGER" "do" -p ./tasks/subdir/subdir_simple.cue verify

    run "$DAGGER" "do" -p ./tasks/subdir/subdir_invalid_path.cue verify
    assert_failure

    run "$DAGGER" "do" -p ./tasks/subdir/subdir_invalid_exec.cue verify
    assert_failure
}

@test "task: #GitPull" {
    "$DAGGER" "do" -p ./tasks/gitpull/exists.cue gitPull
    "$DAGGER" "do" -p ./tasks/gitpull/git_dir.cue verify
    "$DAGGER" "do" -p ./tasks/gitpull/private_repo.cue testContent

    run "$DAGGER" "do" -p ./tasks/gitpull/invalid.cue invalid
    assert_failure
    run "$DAGGER" "do" -p ./tasks/gitpull/bad_remote.cue badremote
    assert_failure
    run "$DAGGER" "do" -p ./tasks/gitpull/bad_ref.cue badref
    assert_failure
}

@test "task: #HTTPFetch" {
    "$DAGGER" "do" -p ./tasks/httpfetch/exist.cue fetch
    run "$DAGGER" "do" -p ./tasks/httpfetch/not_exist.cue fetch
    assert_failure
}

@test "task: #NewSecret" {
    "$DAGGER" "do" -p ./tasks/newsecret/newsecret.cue verify
}

@test "task: #TrimSecret" {
    "$DAGGER" "do" -p ./tasks/trimsecret/trimsecret.cue verify
}

@test "task: #Source" {
    "$DAGGER" "do" -p ./tasks/source/source.cue test
    "$DAGGER" "do" -p ./tasks/source/source_include_exclude.cue test
    "$DAGGER" "do" -p ./tasks/source/source_relative.cue verifyHello

    run "$DAGGER" "do" -p ./tasks/source/source_invalid_path.cue source
    assert_failure

    run "$DAGGER" "do" -p ./tasks/source/source_not_exist.cue source
    assert_failure
}

@test "task: #Merge" {
    "$DAGGER" "do" -p ./tasks/merge/merge.cue test
}

@test "task: #Diff" {
    "$DAGGER" "do" -p ./tasks/diff/diff.cue test
}

@test "task: #Export" {
    "$DAGGER" "do" -p ./tasks/export/export.cue test
}
