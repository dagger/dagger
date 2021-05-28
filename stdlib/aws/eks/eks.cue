package eks

import (
	"dagger.io/dagger/op"
	"dagger.io/aws"
)

// KubeConfig config outputs a valid kube-auth-config for kubectl client
#KubeConfig: {
	// AWS Config
	config: aws.#Config

	// EKS cluster name
	clusterName: string @dagger(input)

	// Kubectl version
	version: *"v1.19.9" | string @dagger(input)

	// kubeconfig is the generated kube configuration file
	kubeconfig: {
		@dagger(output)
		string

		#up: [
			op.#Load & {
				from: aws.#CLI & {
					"config": config
				}
			},

			op.#WriteFile & {
				dest:    "/entrypoint.sh"
				content: #Code
			},

			op.#Exec & {
				always: true
				args: [
					"/bin/bash",
					"--noprofile",
					"--norc",
					"-eo",
					"pipefail",
					"/entrypoint.sh",
				]
				env: {
					EKS_CLUSTER:     clusterName
					KUBECTL_VERSION: version
				}
				mount: {
					"/cache/aws": "cache"
					"/cache/bin": "cache"
				}
			},
			op.#Export & {
				source: "/kubeconfig"
				format: "string"
			},
		]
	}
}
