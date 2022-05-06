setup() {
    load 'helpers'

    common_setup
    cd "$TESTDIR" || exit
}

@test "task: #Pull auth" {
    "$DAGGER" "do" -p ./tasks/pull/pull.cue pull
    "$DAGGER" "do" -p ./tasks/pull/pull_platform.cue
    "$DAGGER" "do" -p ./tasks/pull/pull_auth.cue pull
}

@test "task: #Push" {
    "$DAGGER" "do" -p ./tasks/push/push.cue pullOutputFile
    "$DAGGER" "do" -p ./tasks/push/push_multi_platform.cue
}

@test "task: #ReadFile" {
    "$DAGGER" "do" -p ./tasks/readfile/readfile.cue readfile
}

@test "task: #WriteFile" {
    "$DAGGER" "do" -p ./tasks/writefile/writefile.cue readfile
}

@test "task: #WriteFile failure" {
    run "$DAGGER" "do" -p ./tasks/writefile/writefile_failure_diff_contents.cue readfile
    assert_failure
}

@test "task: #Exec args" {
    cd ./tasks/exec
    "$DAGGER" "do" -p ./args.cue verify
}

@test "task: #Exec env" {
    cd ./tasks/exec
    "$DAGGER" "do" -p ./env.cue verify
}

@test "task: #Exec env secret" {
    cd ./tasks/exec
    "$DAGGER" "do" -p ./env_secret.cue verify
}

@test "task: #Exec hosts" {
    cd ./tasks/exec
    "$DAGGER" "do" -p ./hosts.cue verify
}

@test "task: #Exec mount cache" {
    cd ./tasks/exec
    "$DAGGER" "do" -p ./mount_cache.cue test
}

@test "task: #Exec mount fs" {
    cd ./tasks/exec
    "$DAGGER" "do" -p ./mount_fs.cue test
}

@test "task: #Exec mount secret" {
    cd ./tasks/exec
    TESTSECRET="hello world" "$DAGGER" "do" -p ./mount_secret.cue test
}

@test "task: #Exec mount tmp" {
    cd ./tasks/exec
    "$DAGGER" "do" -p ./mount_tmp.cue verify
}

@test "task: #Exec mount socket" {
    cd ./tasks/exec
    "$DAGGER" "do" -p ./mount_socket.cue verify
}

@test "task: #Exec user" {
    cd ./tasks/exec
    "$DAGGER" "do" -p ./user.cue test
}

@test "task: #Exec workdir" {
    cd ./tasks/exec
    "$DAGGER" "do" -p ./workdir.cue verify
}

@test "task: #Start #Stop" {
    cd ./tasks/exec
    run "$DAGGER" "do" --log-format=plain -l info -p ./start_stop_exec.cue basicTest
    assert_success
    assert_line --partial 'actions.basicTest.start'
    assert_line --regexp 'actions\.basicTest\.sleep \| .*taking a quick nap'
    # order of start and sleep is variable, but Sig and Stop must be last
    assert_line --partial --index 7 'actions.basicTest.sig'
    assert_line --partial --index 9 'actions.basicTest.stop'
}

@test "task: #Start #Stop params" {
    cd ./tasks/exec
    "$DAGGER" "do" -p ./start_stop_exec.cue execParamsTest
}

@test "task: #Start #Stop timeout" {
    cd ./tasks/exec
    "$DAGGER" "do" -p ./start_stop_exec.cue stopTimeoutTest
}

@test "task: #Copy exec" {
    "$DAGGER" "do" -p ./tasks/copy/copy_exec.cue test
}

@test "task: #Copy file" {
    "$DAGGER" "do" -p ./tasks/copy/copy_file.cue test
}

@test "task: #Copy exec invalid" {
    run "$DAGGER" "do" -p ./tasks/copy/copy_exec_invalid.cue test
    assert_failure
}

@test "task: #Mkdir" {
    # Make directory
    "$DAGGER" "do" -p ./tasks/mkdir/mkdir.cue readChecker
}

@test "task: #Mkdir parents" {
    # Create parents
    "$DAGGER" "do" -p ./tasks/mkdir/mkdir_parents.cue readChecker
}

@test "task: #Mkdir parents failure" {
    # Disable parents creation
    run "$DAGGER" "do" -p ./tasks/mkdir/mkdir_failure_disable_parents.cue readChecker
    assert_failure
}

@test "task: #Dockerfile" {
    cd "$TESTDIR"/tasks/dockerfile
    "$DAGGER" "do" -p ./dockerfile.cue verify
}

@test "task: #Dockerfile inlined" {
    cd "$TESTDIR"/tasks/dockerfile
    "$DAGGER" "do" -p ./inlined_dockerfile.cue verify
}

@test "task: #Dockerfile inlined heredoc" {
    cd "$TESTDIR"/tasks/dockerfile
    "$DAGGER" "do" -p ./inlined_dockerfile_heredoc.cue verify
}

@test "task: #Dockerfile path" {
    cd "$TESTDIR"/tasks/dockerfile
    "$DAGGER" "do" -p ./dockerfile_path.cue verify
}

@test "task: #Dockerfile build args" {
    cd "$TESTDIR"/tasks/dockerfile
    "$DAGGER" "do" -p ./build_args.cue build
}

@test "task: #Dockerfile image config" {
    cd "$TESTDIR"/tasks/dockerfile
    "$DAGGER" "do" -p ./image_config.cue build
}

@test "task: #Dockerfile labels" {
    cd "$TESTDIR"/tasks/dockerfile
    "$DAGGER" "do" -p ./labels.cue build
}

@test "task: #Dockerfile platform" {
    cd "$TESTDIR"/tasks/dockerfile
    "$DAGGER" "do" -p ./platform.cue build
}

@test "task: #Dockerfile build auth" {
    cd "$TESTDIR"/tasks/dockerfile
    "$DAGGER" "do" -p ./build_auth.cue build
}

@test "task: #Scratch" {
    "$DAGGER" "do" -p ./tasks/scratch/scratch.cue exec
}

@test "task: #Scratch build" {
    "$DAGGER" "do" -p ./tasks/scratch/scratch_build_scratch.cue build
}

@test "task: #Scratch writefile" {
    "$DAGGER" "do" -p ./tasks/scratch/scratch_writefile.cue readfile
}

@test "task: #Subdir" {
    "$DAGGER" "do" -p ./tasks/subdir/subdir_simple.cue verify
}

@test "task: #Subdir invalid path" {
    run "$DAGGER" "do" -p ./tasks/subdir/subdir_invalid_path.cue verify
    assert_failure
}

@test "task: #Subdir invalid exec" {
    run "$DAGGER" "do" -p ./tasks/subdir/subdir_invalid_exec.cue verify
    assert_failure
}

@test "task: #GitPull" {
    "$DAGGER" "do" -p ./tasks/gitpull/exists.cue gitPull
}

@test "task: #GitPull dir" {
    "$DAGGER" "do" -p ./tasks/gitpull/git_dir.cue verify
}

@test "task: #GitPull private repo" {
    "$DAGGER" "do" -p ./tasks/gitpull/private_repo.cue testContent
}

@test "task: #GitPull invalid" {
    run "$DAGGER" "do" -p ./tasks/gitpull/invalid.cue invalid
    assert_failure
}

@test "task: #GitPull bad remote" {
    run "$DAGGER" "do" -p ./tasks/gitpull/bad_remote.cue badremote
    assert_failure
}

@test "task: #GitPull bad ref" {
    run "$DAGGER" "do" -p ./tasks/gitpull/bad_ref.cue badref
    assert_failure
}

@test "task: #HTTPFetch" {
    "$DAGGER" "do" -p ./tasks/httpfetch/exist.cue fetch
}

@test "task: #HTTPFetch not exist" {
    run "$DAGGER" "do" -p ./tasks/httpfetch/not_exist.cue fetch
    assert_failure
}

@test "task: #DecodeSecret" {
    "$DAGGER" "do" -p ./tasks/decodesecret/decodesecret.cue test
    "$DAGGER" "do" -p ./tasks/decodesecret/decodesecret.cue test --format json
}

@test "task: #NewSecret" {
    run "$DAGGER" "do" -p ./tasks/newsecret/newsecret.cue verify
    assert_line --partial HELLOWORLD
}

@test "task: #TrimSecret" {
    "$DAGGER" "do" -p ./tasks/trimsecret/trimsecret.cue verify
}

@test "task: #Source" {
    "$DAGGER" "do" -p ./tasks/source/source.cue test
}

@test "task: #Source include exclude" {
    "$DAGGER" "do" -p ./tasks/source/source_include_exclude.cue test
}

@test "task: #Source relative" {
    "$DAGGER" "do" -p ./tasks/source/source_relative.cue verifyHello
}

@test "task: #Source invalid path" {
    run "$DAGGER" "do" -p ./tasks/source/source_invalid_path.cue source
    assert_failure
}

@test "task: #Source not exist" {
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

@test "task: #Rm" {
    "$DAGGER" "do" -p ./tasks/rm/rm.cue test
}