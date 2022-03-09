setup() {
    load 'helpers'

    common_setup
}

# @test "task: #Pull" {
#     cd "$TESTDIR"/tasks/pull
#     "$DAGGER" up ./pull.cue
# }
#
# @test "task: #Pull with auth" {
#     cd "$TESTDIR"
#     "$DAGGER" up ./tasks/pull/pull_auth.cue
# }
#
# @test "task: #Push" {
#     cd "$TESTDIR"
#     "$DAGGER" up ./tasks/push/push.cue
# }
#
# @test "task: #ReadFile" {
#     cd "$TESTDIR"/tasks/readfile
#     "$DAGGER" up
# }
#
# @test "task: #WriteFile" {
#     cd "$TESTDIR"/tasks/writefile
#     "$DAGGER" up ./writefile.cue
# }
#
# @test "task: #WriteFile failure: different contents" {
#     cd "$TESTDIR"/tasks/writefile
#     run "$DAGGER" up ./writefile_failure_diff_contents.cue
#     assert_failure
# }
#
# @test "task: #Exec" {
#     cd "$TESTDIR"/tasks/exec
#     "$DAGGER" up ./args.cue
#     "$DAGGER" up ./env.cue
#     "$DAGGER" up ./env_secret.cue
#     "$DAGGER" up ./hosts.cue
#
#     "$DAGGER" up ./mount_cache.cue
#     "$DAGGER" up ./mount_fs.cue
#     TESTSECRET="hello world" "$DAGGER" up ./mount_secret.cue
#     "$DAGGER" up ./mount_tmp.cue
#     "$DAGGER" up ./mount_service.cue
#
#     "$DAGGER" up ./user.cue
#     "$DAGGER" up ./workdir.cue
# }
#
# @test "task: #Copy" {
#     cd "$TESTDIR"/tasks/copy
#     "$DAGGER" up ./copy_exec.cue
#     "$DAGGER" up ./copy_file.cue
#
#     run "$DAGGER" up ./copy_exec_invalid.cue
#     assert_failure
# }
#
# @test "task: #Mkdir" {
#     # Make directory
#     cd "$TESTDIR"/tasks/mkdir
#     "$DAGGER" up ./mkdir.cue
#
#     # Create parents
#     cd "$TESTDIR"/tasks/mkdir
#     "$DAGGER" up ./mkdir_parents.cue
#
#     # Disable parents creation
#     cd "$TESTDIR"/tasks/mkdir
#     run "$DAGGER" up ./mkdir_failure_disable_parents.cue
#     assert_failure
# }
#
# @test "task: #Dockerfile" {
#     cd "$TESTDIR"/tasks/dockerfile
#
#     "$DAGGER" up ./dockerfile.cue
#     "$DAGGER" up ./inlined_dockerfile.cue
#     "$DAGGER" up ./inlined_dockerfile_heredoc.cue
#     "$DAGGER" up ./dockerfile_path.cue
#     "$DAGGER" up ./build_args.cue
#     "$DAGGER" up ./image_config.cue
#     "$DAGGER" up ./labels.cue
#     "$DAGGER" up ./platform.cue
#     "$DAGGER" up ./build_auth.cue
# }
# @test "task: #Scratch" {
#     cd "$TESTDIR"/tasks/scratch
#     "$DAGGER" up ./scratch.cue -l debug
#     "$DAGGER" up ./scratch_build_scratch.cue -l debug
#     "$DAGGER" up ./scratch_writefile.cue -l debug
# }
#
# @test "task: #Subdir" {
#     cd "$TESTDIR"/tasks/subdir
#     "$DAGGER" up ./subdir_simple.cue
#
#     run "$DAGGER" up ./subdir_invalid_path.cue
#     assert_failure
#
#     run "$DAGGER" up ./subdir_invalid_exec.cue
#     assert_failure
# }
#
# @test "task: #GitPull" {
#     cd "$TESTDIR"
#     "$DAGGER" up ./tasks/gitpull/exists.cue
#     "$DAGGER" up ./tasks/gitpull/git_dir.cue
#     "$DAGGER" up ./tasks/gitpull/private_repo.cue
#
#     run "$DAGGER" up ./tasks/gitpull/invalid.cue
#     assert_failure
#     run "$DAGGER" up ./tasks/gitpull/bad_remote.cue
#     assert_failure
#     run "$DAGGER" up ./tasks/gitpull/bad_ref.cue
#     assert_failure
# }
#
# @test "task: #HTTPFetch" {
#     cd "$TESTDIR"
#     "$DAGGER" up ./tasks/httpfetch/exist.cue
#     run "$DAGGER" up ./tasks/httpfetch/not_exist.cue
#     assert_failure
# }
#
# @test "task: #NewSecret" {
#     cd "$TESTDIR"/tasks/newsecret
#
#     "$DAGGER" up ./newsecret.cue
# }
#
# @test "task: #TrimSecret" {
#     cd "$TESTDIR"/tasks/trimsecret
#
#     "$DAGGER" up ./trimsecret.cue
# }
#
# @test "task: #Source" {
#     cd "$TESTDIR"/tasks/source
#     "$DAGGER" up ./source.cue
#     "$DAGGER" up ./source_include_exclude.cue
#     "$DAGGER" up ./source_relative.cue
#
#     run "$DAGGER" up ./source_invalid_path.cue
#     assert_failure
#
#     run "$DAGGER" up ./source_not_exist.cue
#     assert_failure
# }
