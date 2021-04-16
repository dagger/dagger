setup() {
    load 'helpers'

    common_setup
}

@test "stdlib: alpine" {
    "$DAGGER" compute "$TESTDIR"/stdlib/alpine
}

@test "stdlib: react" {
    "$DAGGER" compute "$TESTDIR"/stdlib/js/react --input-dir TestData="$TESTDIR"/stdlib/js/react/testdata
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
    skip "disabled"
    skip_unless_local_kube

    "$DAGGER" compute "$TESTDIR"/stdlib/kubernetes --input-dir kubeconfig=~/.kube
}

@test "stdlib: helm" {
    skip "disabled"
    skip_unless_local_kube

    "$DAGGER" compute "$TESTDIR"/stdlib/kubernetes/helm --input-dir kubeconfig=~/.kube --input-dir TestHelmSimpleChart.deploy.chartSource="$TESTDIR"/stdlib/kubernetes/helm/testdata/mychart
}
