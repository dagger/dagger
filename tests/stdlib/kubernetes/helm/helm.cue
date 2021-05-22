package helm

import (
	"dagger.io/dagger"
	"dagger.io/file"
	"dagger.io/kubernetes/helm"
)

// We assume that a kinD cluster is running locally
// To deploy a local KinD cluster, follow this link : https://kind.sigs.k8s.io/docs/user/quick-start/
kubeconfig: dagger.#Artifact

// Retrive kubeconfig
config: file.#Read & {
	filename: "config"
	from:     kubeconfig
}

// Deploy user local chart
TestHelmSimpleChart: {
	// Deploy chart
	deploy: helm.#Chart & {
		name:        "dagger-test-helm-simple-chart-\(random)"
		namespace:   "dagger-test"
		kubeconfig:  config.contents
		chartSource: dagger.#Artifact
	}

	// Verify deployment
	verify: #VerifyHelm & {
		chartName: deploy.name
		namespace: deploy.namespace
	}
}

// Deploy remote chart
TestHelmRepoChart: {
	// Deploy chart
	deploy: helm.#Chart & {
		name:       "dagger-test-helm-repository-\(random)"
		namespace:  "dagger-test"
		kubeconfig: config.contents
		chart:      "redis"
	}

	// Verify deployment
	verify: #VerifyHelm & {
		chartName: deploy.name
		namespace: deploy.namespace
	}
}
