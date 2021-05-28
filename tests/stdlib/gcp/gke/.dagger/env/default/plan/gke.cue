package gke

import (
	"dagger.io/gcp"
	"dagger.io/gcp/gke"
	"dagger.io/kubernetes"
	"dagger.io/dagger/op"
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
