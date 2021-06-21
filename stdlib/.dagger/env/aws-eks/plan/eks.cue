package eks

import (
	"dagger.io/aws"
	"dagger.io/aws/eks"
	"dagger.io/kubernetes"
	"dagger.io/dagger/op"
)

TestConfig: awsConfig: aws.#Config & {
	region: "us-east-2"
}

TestCluster: eks.#KubeConfig & {
	config:      TestConfig.awsConfig
	clusterName: *"dagger-example-eks-cluster" | string
}

TestEks: {
	#GetPods:
		"""
			kubectl get pods -A
			"""

	#up: [
		op.#Load & {
			from: kubernetes.#Kubectl
		},

		op.#WriteFile & {
			dest:    "/kubeconfig"
			content: TestCluster.kubeconfig
		},

		op.#WriteFile & {
			dest:    "/getPods.sh"
			content: #GetPods
		},

		op.#Exec & {
			always: true
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/getPods.sh",
			]
			env: KUBECONFIG: "/kubeconfig"
		},
	]
}
