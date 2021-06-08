package main

import (
	"dagger.io/dagger"
	"dagger.io/kubernetes/helm"
)

// We assume that a kinD cluster is running locally
// To deploy a local KinD cluster, follow this link : https://kind.sigs.k8s.io/docs/user/quick-start/
kubeconfig: dagger.#Secret @dagger(input)

// Deploy user local chart
TestHelmSimpleChart: {
	random: #Random & {}

	// Deploy chart
	deploy: helm.#Chart & {
		name:         "dagger-test-helm-simple-chart-\(random.out)"
		namespace:    "dagger-test"
		"kubeconfig": kubeconfig
		chartSource:  dagger.#Artifact
	}

	// Verify deployment
	verify: #VerifyHelm & {
		chartName: deploy.name
		namespace: deploy.namespace
	}
}

// Deploy remote chart
TestHelmRepoChart: {
	random: #Random & {}

	// Deploy chart
	deploy: helm.#Chart & {
		name:         "dagger-test-helm-repository-\(random.out)"
		namespace:    "dagger-test"
		"kubeconfig": kubeconfig
		chart:        "redis"
	}

	// Verify deployment
	verify: #VerifyHelm & {
		chartName: deploy.name
		namespace: deploy.namespace
	}
}
