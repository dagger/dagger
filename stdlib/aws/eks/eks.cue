// AWS Elastic Kubernetes Service (EKS)
package eks

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/aws"
)

// KubeConfig config outputs a valid kube-auth-config for kubectl client
#KubeConfig: {
	// AWS Config
	config: aws.#Config

	// EKS cluster name
	clusterName: string & dagger.#Input

	// Kubectl version
	version: *"v1.19.9" | string & dagger.#Input

	// kubeconfig is the generated kube configuration file
	kubeconfig: {
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
	} & dagger.#Output
}
