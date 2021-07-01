package gke

import (
	"alpha.dagger.io/gcp"
	"alpha.dagger.io/gcp/gke"
	"alpha.dagger.io/kubernetes"
	"alpha.dagger.io/dagger/op"
)

TestConfig: gcpConfig: gcp.#Config

TestCluster: gke.#KubeConfig & {
	config:      TestConfig.gcpConfig
	clusterName: "test-cluster"
}

TestGKE: #up: [
	op.#Load & {
		from: kubernetes.#Kubectl
	},

	op.#WriteFile & {
		dest:    "/kubeconfig"
		content: TestCluster.kubeconfig
	},

	op.#Exec & {
		always: true
		args: ["kubectl", "get", "nodes"]
		env: KUBECONFIG: "/kubeconfig"
	},
]
