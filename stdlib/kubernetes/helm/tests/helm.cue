package helm

import (
	"alpha.dagger.io/random"
	"alpha.dagger.io/dagger"
)

// We assume that a kinD cluster is running locally
// To deploy a local KinD cluster, follow this link : https://kind.sigs.k8s.io/docs/user/quick-start/
TestKubeconfig: string & dagger.#Input

TestChartSource: dagger.#Artifact & dagger.#Input

// Deploy user local chart
TestHelmSimpleChart: {
	suffix: random.#String & {
		seed:   "simple"
		length: 5
	}

	// Deploy chart
	deploy: #Chart & {
		name:        "dagger-test-inline-\(suffix.out)"
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
		seed:   "repo"
		length: 5
	}

	// Deploy remote chart
	deploy: #Chart & {
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
