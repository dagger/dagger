package main

import (
	"dagger.io/dagger"
	"dagger.io/kubernetes/helm"
	"dagger.io/random"
)

// We assume that a kinD cluster is running locally
// To deploy a local KinD cluster, follow this link : https://kind.sigs.k8s.io/docs/user/quick-start/
kubeconfig: dagger.#Secret @dagger(input)

// Deploy user local chart
TestHelmSimpleChart: {
	suffix: random.#String & {
		seed: "simple"
	}

	// Deploy chart
	deploy: helm.#Chart & {
		name:         "dagger-test-helm-simple-chart-\(suffix.out)"
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
	suffix: random.#String & {
		seed: "repo"
	}

	// Deploy chart
	deploy: helm.#Chart & {
		name:         "dagger-test-helm-repository-\(suffix.out)"
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
