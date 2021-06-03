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
    "$DAGGER" up -w "$TESTDIR"/stdlib/netlify/
}

@test "stdlib: kubernetes" {
    skip_unless_local_kube

    "$DAGGER" init
    dagger_new_with_plan kubernetes "$TESTDIR"/stdlib/kubernetes/

    run "$DAGGER" input -e "kubernetes" secret kubeconfig -f ~/.kube/config
    assert_success

    run "$DAGGER" up -e "kubernetes"
    assert_success
}

@test "stdlib: kustomize" {
    "$DAGGER" compute "$TESTDIR"/stdlib/kubernetes/kustomize --input-dir TestKustomize.kustom.source="$TESTDIR"/stdlib/kubernetes/kustomize/testdata
}

@test "stdlib: helm" {
    skip_unless_local_kube

    "$DAGGER" init
    dagger_new_with_plan helm "$TESTDIR"/stdlib/kubernetes/helm

    run "$DAGGER" input -e "helm" secret kubeconfig -f ~/.kube/config
    assert_success

    cp -R "$TESTDIR"/stdlib/kubernetes/helm/testdata/mychart "$DAGGER_WORKSPACE"/testdata
    run "$DAGGER" input -e "helm" dir TestHelmSimpleChart.deploy.chartSource "$DAGGER_WORKSPACE"/testdata
    assert_success

    run "$DAGGER" up -e "helm"
    assert_success
}

@test "stdlib: aws: s3" {
    "$DAGGER" up -w "$TESTDIR"/stdlib/aws/s3
}

@test "stdlib: aws: eks" {
    "$DAGGER" up -w "$TESTDIR"/stdlib/aws/eks
}

@test "stdlib: aws: ecr" {
    "$DAGGER" up -w "$TESTDIR"/stdlib/aws/ecr
}

@test "stdlib: gcp: gke" {
    "$DAGGER" up -w "$TESTDIR"/stdlib/gcp/gke
}

@test "stdlib: gcp: gcr" {
    "$DAGGER" up -w "$TESTDIR"/stdlib/gcp/gcr
}

@test "stdlib: docker: build" {
    "$DAGGER" compute "$TESTDIR"/stdlib/docker/build/ --input-dir source="$TESTDIR"/stdlib/docker/build
}

@test "stdlib: docker: dockerfile" {
    "$DAGGER" compute "$TESTDIR"/stdlib/docker/dockerfile/ --input-dir source="$TESTDIR"/stdlib/docker/dockerfile/testdata
}

@test "stdlib: docker: push-and-pull" {
    skip_unless_secrets_available "$TESTDIR"/stdlib/docker/push-pull/inputs.yaml

    # check that they succeed with the credentials
    run "$DAGGER" compute --input-yaml "$TESTDIR"/stdlib/docker/push-pull/inputs.yaml --input-dir source="$TESTDIR"/stdlib/docker/push-pull/testdata "$TESTDIR"/stdlib/docker/push-pull/
    assert_success
}

@test "stdlib: docker: run" {
    # Simple run
    run "$DAGGER" up -w "$TESTDIR"/stdlib/docker/run/simple/
    assert_success

    # Protected ssh key
    run "$DAGGER" up -w "$TESTDIR"/stdlib/docker/run/passphrase/
    assert_success

    # Protected ssh key with wrong passphrase
    run "$DAGGER" up -w "$TESTDIR"/stdlib/docker/run/wrong-passphrase/
    assert_failure
}

@test "stdlib: terraform" {
    skip_unless_secrets_available "$TESTDIR"/stdlib/terraform/s3/inputs.yaml

    "$DAGGER" init
    dagger_new_with_plan terraform "$TESTDIR"/stdlib/terraform/s3

    cp -R "$TESTDIR"/stdlib/terraform/s3/testdata "$DAGGER_WORKSPACE"/testdata
    "$DAGGER" -e terraform input dir TestData "$DAGGER_WORKSPACE"/testdata
    sops -d "$TESTDIR"/stdlib/terraform/s3/inputs.yaml | "$DAGGER" -e "terraform" input yaml "" -f -

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
