package helm

import (
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
		seed:   "simple"
		length: 5
	}

	// Deploy chart
	deployment: #Chart & {
		name:        "dagger-test-inline-\(suffix.out)"
		namespace:   "dagger-test"
		kubeconfig:  TestKubeconfig
		chartSource: TestChartSource
	}

	// Verify deployment
	test: #VerifyHelm & {
		chartName: deployment.name
		namespace: deployment.namespace
	}
}

// Deploy remote chart
TestHelmRepoChart: {
	suffix: random.#String & {
		seed:   "repo"
		length: 5
	}

	// Deploy remote chart
	deployment: #Chart & {
		name:       "dagger-test-repository-\(suffix.out)"
		namespace:  "dagger-test"
		kubeconfig: TestKubeconfig
		repository: "https://charts.bitnami.com/bitnami"
		chart:      "redis"
	}

	// Verify deployment
	verify: #VerifyHelm & {
		chartName: deployment.name
		namespace: deployment.namespace
	}
}
