setup() {
    load 'helpers'

    common_setup
}

# FIXME: move to universe/universe.bats
# Assigned to: <ADD YOUR NAME HERE>
# Changes in https://github.com/dagger/dagger/pull/628
@test "stdlib: docker: push-and-pull" {
    skip_unless_secrets_available "$TESTDIR"/stdlib/docker/push-pull/inputs.yaml

    # check that they succeed with the credentials
    run "$DAGGER" compute --input-yaml "$TESTDIR"/stdlib/docker/push-pull/inputs.yaml --input-dir source="$TESTDIR"/stdlib/docker/push-pull/testdata "$TESTDIR"/stdlib/docker/push-pull/
    assert_success
}
