package helm

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/alpine"
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

// Generate random string
random: {
	string
	#up: [
		op.#Load & {from: alpine.#Image},
		op.#Exec & {
			args: ["sh", "-c", "cat /dev/urandom | tr -dc 'a-z' | fold -w 10 | head -n 1 | tr -d '\n' > /rand"]
		},
		op.#Export & {
			source: "/rand"
		},
	]
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
