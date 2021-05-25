setup() {
    load 'helpers'

    common_setup
}

@test "stdlib: alpine" {
    "$DAGGER" compute "$TESTDIR"/stdlib/alpine
}

@test "stdlib: yarn" {
    "$DAGGER" compute "$TESTDIR"/stdlib/js/yarn --input-dir TestData="$TESTDIR"/stdlib/js/yarn/testdata
}

@test "stdlib: go" {
    "$DAGGER" compute "$TESTDIR"/stdlib/go --input-dir TestData="$TESTDIR"/stdlib/go/testdata
}

@test "stdlib: file" {
    "$DAGGER" compute "$TESTDIR"/stdlib/file
}

@test "stdlib: netlify" {
    skip_unless_secrets_available "$TESTDIR"/stdlib/netlify/inputs.yaml

    "$DAGGER" compute "$TESTDIR"/stdlib/netlify --input-yaml "$TESTDIR"/stdlib/netlify/inputs.yaml
}

@test "stdlib: kubernetes" {
    skip_unless_local_kube

    "$DAGGER" compute "$TESTDIR"/stdlib/kubernetes --input-dir kubeconfig=~/.kube
}

@test "stdlib: kustomize" {
    "$DAGGER" compute "$TESTDIR"/stdlib/kubernetes/kustomize --input-dir TestKustomize.kustom.source="$TESTDIR"/stdlib/kubernetes/kustomize/testdata
}

@test "stdlib: helm" {
    skip_unless_local_kube

    "$DAGGER" compute "$TESTDIR"/stdlib/kubernetes/helm --input-dir kubeconfig=~/.kube --input-dir TestHelmSimpleChart.deploy.chartSource="$TESTDIR"/stdlib/kubernetes/helm/testdata/mychart
}

@test "stdlib: aws: s3" {
    skip_unless_secrets_available "$TESTDIR"/stdlib/aws/inputs.yaml

    "$DAGGER" compute "$TESTDIR"/stdlib/aws/s3 --input-dir TestDirectory="$TESTDIR"/stdlib/aws/s3/testdata --input-yaml "$TESTDIR"/stdlib/aws/inputs.yaml
}

@test "stdlib: aws: eks" {
    skip_unless_secrets_available "$TESTDIR"/stdlib/aws/inputs.yaml

    "$DAGGER" compute "$TESTDIR"/stdlib/aws/eks --input-yaml "$TESTDIR"/stdlib/aws/inputs.yaml
}

@test "stdlib: aws: ecr" {
    skip_unless_secrets_available "$TESTDIR"/stdlib/aws/inputs.yaml

    "$DAGGER" compute "$TESTDIR"/stdlib/aws/ecr --input-yaml "$TESTDIR"/stdlib/aws/inputs.yaml
}

@test "stdlib: gcp: gke" {
    skip_unless_secrets_available "$TESTDIR"/stdlib/gcp/inputs.yaml

    "$DAGGER" compute "$TESTDIR"/stdlib/gcp/gke --input-yaml "$TESTDIR"/stdlib/gcp/inputs.yaml
}

@test "stdlib: gcp: gcr" {
    skip_unless_secrets_available "$TESTDIR"/stdlib/gcp/inputs.yaml

    "$DAGGER" compute "$TESTDIR"/stdlib/gcp/gcr --input-yaml "$TESTDIR"/stdlib/gcp/inputs.yaml
}

@test "stdlib: docker-build" {
    "$DAGGER" compute "$TESTDIR"/stdlib/docker/build/ --input-dir source="$TESTDIR"/stdlib/docker/build
}

@test "stdlib: docker-dockerfile" {
    "$DAGGER" compute "$TESTDIR"/stdlib/docker/dockerfile/ --input-dir source="$TESTDIR"/stdlib/docker/dockerfile/testdata
}

@test "stdlib: docker-push-and-pull" {
    skip_unless_secrets_available "$TESTDIR"/stdlib/docker/push-pull/inputs.yaml

    # check that they succeed with the credentials
    run "$DAGGER" compute --input-yaml "$TESTDIR"/stdlib/docker/push-pull/inputs.yaml --input-dir source="$TESTDIR"/stdlib/docker/push-pull/testdata "$TESTDIR"/stdlib/docker/push-pull/
    assert_success
}

@test "stdlib: terraform" {
    skip_unless_secrets_available "$TESTDIR"/stdlib/aws/inputs.yaml

    "$DAGGER" init
    dagger_new_with_plan terraform "$TESTDIR"/stdlib/terraform/s3

    cp -R "$TESTDIR"/stdlib/terraform/s3/testdata "$DAGGER_WORKSPACE"/testdata
    "$DAGGER" -e terraform input dir TestData "$DAGGER_WORKSPACE"/testdata
    sops -d "$TESTDIR"/stdlib/aws/inputs.yaml | "$DAGGER" -e "terraform" input yaml "" -f -

    # it must fail because of a missing var
    run "$DAGGER" up -e terraform
    assert_failure

    # add the var and try again
    "$DAGGER" -e terraform input text TestTerraform.apply.tfvars.input "42"
    run "$DAGGER" up -e terraform
    assert_success

    # ensure the tfvar was passed correctly
    run "$DAGGER" query -e terraform \
        TestTerraform.apply.output.input.value  -f text
    assert_success
    assert_output "42"

    # ensure the random value is always the same
    # this proves we're effectively using the s3 backend
    run "$DAGGER" query -e terraform \
        TestTerraform.apply.output.random.value  -f json
    assert_success
    assert_output "36"
}
