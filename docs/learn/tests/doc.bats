setup() {
    load 'helpers'

    common_setup
}

@test "doc-102" {
    dagger -e 102 up
}

@test "doc-106" {
    dagger -e 106 up
}

@test "doc-107-kind" {
    skip_unless_local_kube

    # Copy deployment to sandbox
    copy_to_sandbox 107-kind-basic 107-kind

    # Set kubeconfig
    dagger -w "$DAGGER_SANDBOX" -e 107-kind-basic input text kubeconfig -f "$HOME"/.kube/config

    # Execute test
    dagger -w "$DAGGER_SANDBOX" -e 107-kind-basic up

    # Unset kubeconfig
    dagger -w "$DAGGER_SANDBOX" -e 107-kind-basic input unset kubeconfig

    # Copy deployment to sandbox
    copy_to_sandbox 107-kind-deployment 107-kind

    # Set kubeconfig
    dagger -w "$DAGGER_SANDBOX" -e 107-kind-deployment input text kubeconfig -f "$HOME"/.kube/config

    # Execute test
    dagger -w "$DAGGER_SANDBOX" -e 107-kind-deployment up

    # Unset kubeconfig
    dagger -w "$DAGGER_SANDBOX" -e 107-kind-deployment input unset kubeconfig

    # Copy deployment to sandbox
    copy_to_sandbox 107-kind-cue-kube-manifest 107-kind

    # Set kubeconfig
    dagger -w "$DAGGER_SANDBOX" -e 107-kind-cue-kube-manifest input text kubeconfig -f "$HOME"/.kube/config

    # Execute test
    dagger -w "$DAGGER_SANDBOX" -e 107-kind-cue-kube-manifest up

    # Unset kubeconfig
    dagger -w "$DAGGER_SANDBOX" -e 107-kind-cue-kube-manifest input unset kubeconfig

}