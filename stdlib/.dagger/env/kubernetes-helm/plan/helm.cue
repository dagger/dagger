package main

import (
	"alpha.dagger.io/kubernetes/helm"
	"alpha.dagger.io/random"
	"alpha.dagger.io/dagger"
)

// We assume that a kinD cluster is running locally
// To deploy a local KinD cluster, follow this link : https://kind.sigs.k8s.io/docs/user/quick-start/
TestKubeconfig: string @dagger(input)

TestChartSource: dagger.#Artifact @dagger(input)

// Deploy user local chart
TestHelmSimpleChart: {
	suffix: random.#String & {
		seed: "simple"
	}

	// Deploy chart
	deploy: helm.#Chart & {
		name:        "dagger-test-inline-chart-\(suffix.out)"
		namespace:   "dagger-test"
		kubeconfig:  TestKubeconfig
		chartSource: TestChartSource
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

	// Deploy remote chart
	deploy: helm.#Chart & {
		name:       "dagger-test-repository-\(suffix.out)"
		namespace:  "dagger-test"
		kubeconfig: TestKubeconfig
		repository: "https://charts.bitnami.com/bitnami"
		chart:      "redis"
	}

	// Verify deployment
	verify: #VerifyHelm & {
		chartName: deploy.name
		namespace: deploy.namespace
	}
}
