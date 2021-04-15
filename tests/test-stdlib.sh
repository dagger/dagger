#!/usr/bin/env bash
set -o errexit -o errtrace -o functrace -o nounset -o pipefail

# Test Directory
d=$(cd "$(dirname "${BASH_SOURCE[0]:-$PWD}")" 2>/dev/null 1>&2 && pwd)

test::stdlib(){
  local dagger="$1"

  test::one "stdlib: alpine" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/alpine
  test::one "stdlib: react" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/js/react --input-dir TestData="$d"/stdlib/js/react/testdata
  test::one "stdlib: go" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/go --input-dir TestData="$d"/stdlib/go/testdata
  test::one "stdlib: file" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/file
  test::secret "$d"/stdlib/netlify/inputs.yaml "stdlib: netlify" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/netlify

  # Check if there is a kubeconfig and if it for a kind cluster
  if [ -f ~/.kube/config ] && grep -q "kind" ~/.kube/config &> /dev/null; then
      test::one "stdlib: kubernetes" \
      "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/kubernetes --input-dir kubeconfig=~/.kube
      test::one "stdlib: helm" \
        "$dagger" "${DAGGER_BINARY_ARGS[@]}" compute "$d"/stdlib/kubernetes/helm --input-dir kubeconfig=~/.kube --input-dir TestHelmSimpleChart.deploy.chartSource="$d"/stdlib/kubernetes/helm/testdata/mychart
  else
    logger::warning "Skip kubernetes test: local cluster not available"
  fi

}
