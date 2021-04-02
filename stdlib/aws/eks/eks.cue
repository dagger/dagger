package eks

import (
	"dagger.io/dagger"
	"dagger.io/llb"
	"dagger.io/aws"
)

// KubeConfig config outputs a valid kube-auth-config for kubectl client
#KubeConfig: {
	// AWS Config
	config: aws.#Config

	// EKS cluster name
	clusterName: string

	// Kubectl version
	version: *"v1.19.9" | string

	// kubeconfig is the generated kube configuration file
	kubeconfig: {
		dagger.#Secret

		#up: [
			llb.#Load & {
				from: aws.#CLI
			},
			llb.#WriteFile & {
				dest:    "/entrypoint.sh"
				content: #Code
			},
			llb.#Exec & {
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
					AWS_CONFIG_FILE:       "/cache/aws/config"
					AWS_ACCESS_KEY_ID:     config.accessKey
					AWS_SECRET_ACCESS_KEY: config.secretKey
					AWS_DEFAULT_REGION:    config.region
					AWS_REGION:            config.region
					AWS_DEFAULT_OUTPUT:    "json"
					AWS_PAGER:             ""
					EKS_CLUSTER:           clusterName
					KUBECTL_VERSION:       version
				}
				mount: {
					"/cache/aws": "cache"
					"/cache/bin": "cache"
				}
			},
			llb.#Export & {
				source: "/kubeconfig"
				format: "string"
			},
		]
	}
}
