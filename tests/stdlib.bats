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

    "$DAGGER" new --plan-dir "$TESTDIR"/stdlib/netlify netlify
    sops -d "$TESTDIR"/stdlib/netlify/inputs.yaml | "$DAGGER" -d "netlify" input yaml "" -f -
    "$DAGGER" up -d "netlify"

    url=$("$DAGGER" query -l error -f text -d "netlify" TestNetlify.deploy.url)
    run curl -sSf "$url"
    assert_success

    # bring the site down, ensure curl fails
    "$DAGGER" down -d "netlify"
    run curl -sSf "$url"
    assert_failure
}

@test "stdlib: kubernetes" {
    skip_unless_local_kube

    "$DAGGER" compute "$TESTDIR"/stdlib/kubernetes --input-dir kubeconfig=~/.kube
}

@test "stdlib: helm" {
    skip_unless_local_kube

    "$DAGGER" compute "$TESTDIR"/stdlib/kubernetes/helm --input-dir kubeconfig=~/.kube --input-dir TestHelmSimpleChart.deploy.chartSource="$TESTDIR"/stdlib/kubernetes/helm/testdata/mychart
}

@test "stdlib: s3" {
    skip_unless_secrets_available "$TESTDIR"/stdlib/aws/inputs.yaml

    "$DAGGER" compute "$TESTDIR"/stdlib/aws/s3 --input-dir TestDirectory="$TESTDIR"/stdlib/aws/s3/testdata --input-yaml "$TESTDIR"/stdlib/aws/inputs.yaml
}
@test "stdlib: aws" {
    skip_unless_secrets_available "$TESTDIR"/stdlib/aws/inputs.yaml

    "$DAGGER" compute "$TESTDIR"/stdlib/aws/eks --input-yaml "$TESTDIR"/stdlib/aws/inputs.yaml
}