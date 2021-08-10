setup() {
    load 'helpers'

    common_setup
}

@test "cue-sanity-check" {
    dagger -e sanity-check up
}


@test "os" {
    dagger -e os up
}

@test "go" {
    dagger -e go up
}

@test "js/yarn" {
    dagger -e js-yarn up
}

@test "java/maven" {
    dagger -e java-maven up
}

@test "alpine" {
    dagger -e alpine up
}

@test "netlify" {
    dagger -e netlify up
}

@test "git" {
    dagger -e git up
}

@test "os.#Container" {
    dagger -e os-container up
}

@test "aws: ecr" {
    dagger -e aws-ecr up
}

@test "aws: s3" {
    dagger -e aws-s3 up
}

@test "aws: eks" {
    dagger -e aws-eks up
}

@test "docker run: local" {
    skip "Not implemented yet + missing inputs leading to failure"  
    # dagger -e docker-run-local up
}

@test "docker build" {
    dagger -e docker-build up
}

@test "docker push and pull" {
  skip "An occasional data race condition happen in the CI. Must be fix before execute that test"
  # Push image
  dagger -e docker-push up

  # Get image reference
  dagger -e docker-pull input text ref "$(dagger -e docker-push query -c TestPush.push.ref | tr -d '\n' | tr -d '\"')"

  # Pull image
  dagger -e docker-pull up
}

@test "docker push: multi registry" {
 skip "An occasional data race condition happen in the CI. Must be fix before execute that test"
 run dagger -e docker-push-multi-registry up
}

@test "docker push: invalid credential" {
  # Push image (SHOULD FAIL)
  run dagger -e docker-push-invalid-creds up
  assert_failure
}

@test "docker command: ssh" {
    dagger -e docker-command-ssh up
}

@test "docker command: ssh with key passphrase" {
    dagger -e docker-command-ssh-key-passphrase up
}

@test "docker command: ssh with wrong key passphrase" {
    run dagger -e docker-command-ssh-wrong-key-passphrase up
    assert_failure
}

@test "docker compose" {
    dagger -e docker-compose up
}

@test "docker run: ssh" {
    dagger -e docker-run-ssh up
}

@test "kubernetes: deployment" {
    skip_unless_local_kube

    # Copy deployment to sandbox
    copy_to_sandbox kubernetes-deployment kubernetes

    # Set kubeconfig
    dagger -w "$DAGGER_SANDBOX" -e kubernetes-deployment input text TestKubeconfig -f "$HOME"/.kube/config

    dagger -w "$DAGGER_SANDBOX" -e kubernetes-deployment up

    # Unset kubeconfig
    dagger -w "$DAGGER_SANDBOX" -e kubernetes-deployment input unset TestKubeconfig
}

@test "kubernetes: kustomize" {
    dagger -e kubernetes-kustomize up
}

@test "kubernetes: helm" {
    skip_unless_local_kube

    # Copy deployment to sandbox
    copy_to_sandbox kubernetes-helm kubernetes

    # Set kubeconfig
    dagger -w "$DAGGER_SANDBOX" -e kubernetes-helm input text TestKubeconfig -f "$HOME"/.kube/config

    dagger -w "$DAGGER_SANDBOX" -e kubernetes-helm up

    # Unset kubeconfig
    dagger -w "$DAGGER_SANDBOX" -e kubernetes-helm input unset TestKubeconfig
}

@test "google cloud: gcr" {
    dagger -e google-gcr up
}

@test "google cloud: gcs" {
    dagger -e google-gcs up
}

@test "google cloud: gke" {
    dagger -e google-gke up
}

@test "google cloud: cloudrun" {
    dagger -e google-cloudrun up
}

@test "terraform" {
    # it must fail because of a missing var
    run dagger -e terraform up
    assert_failure

    # Copy deployment to sandbox
    copy_to_sandbox terraform terraform

    # Add the var and try again
    run dagger -w "$DAGGER_SANDBOX" -e terraform input text TestTerraform.apply.tfvars.input "42"
    run dagger -w "$DAGGER_SANDBOX" -e terraform up
    assert_success

     # ensure the tfvar was passed correctly
    run dagger -w "$DAGGER_SANDBOX" query -e terraform TestTerraform.apply.output.input.value  -f text
    assert_success
    assert_output "42"

    # ensure the random value is always the same
    # this proves we're effectively using the s3 backend
    run dagger -w "$DAGGER_SANDBOX" query -e terraform TestTerraform.apply.output.random.value  -f json
    assert_success
    assert_output "36"

    # Unset input
    run dagger -w "$DAGGER_SANDBOX" -e terraform input unset TestTerraform.apply.tfvars.input
    assert_success
}

# @test "azure-resourcegroup" {
#     dagger -e azure-resourcegroup up
# }