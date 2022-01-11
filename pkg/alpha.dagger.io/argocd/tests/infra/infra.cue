package infra

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/kubernetes"
)

TestKubeconfig: dagger.#Input & {string}

TestArgoInfra: kubernetes.#Resources & {
	kubeconfig: TestKubeconfig
	namespace:  "argocd"
	url:        "https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml"
}
