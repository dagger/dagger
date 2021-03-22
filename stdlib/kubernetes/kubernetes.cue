package kubernetes

import (
	"dagger.io/llb"
	"dagger.io/dagger"
	"dagger.io/alpine"
)

#Kubectl: {

	version: *"v1.19.9" | string

	#code: #"""
		[ -e /usr/local/bin/kubectl ] || {
			curl -sfL https://dl.k8s.io/${KUBECTL_VERSION}/bin/linux/amd64/kubectl -o /usr/local/bin/kubectl \
			&& chmod +x /usr/local/bin/kubectl
		}
		"""#

	#compute: [
		llb.#Load & {
			from: alpine.#Image & {
				package: bash:      "=5.1.0-r0"
				package: jq:        "=1.6-r1"
				package: curl:      "=7.74.0-r1"
			}
		},
		llb.#WriteFile & {
			dest:    "/entrypoint.sh"
			content: #code
		},
		llb.#Exec & {
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/entrypoint.sh",
			]
			env: {
				KUBECTL_VERSION: version
			}
		}
	]
}

// Apply a Kubernetes configuration
#Apply: {
	// Kubernetes config to deploy
	source: string | dagger.#Artifact

	// Kubernetes Namespace to deploy to
	namespace: string

	// Version of kubectl client
	version: *"v1.19.9" | string

	// Kube config file
	kubeconfig: dagger.#Secret

	#code: #"""
		kubectl create namespace "$KUBE_NAMESPACE" || true
		kubectl --namespace "$KUBE_NAMESPACE" apply -R -f /source
		"""#

	#compute: [
		llb.#Load & {
			from: #Kubectl & { "version": version }
		},
		llb.#WriteFile & {
			dest:    "/entrypoint.sh"
			content: #code
		},
		llb.#WriteFile & {
			dest:    "/kubeconfig"
			content: kubeconfig
			mode:    0o600
		},
		if (source & string) != _|_ {
			llb.#WriteFile & {
				dest: "/source"
				content: source
			}
		}
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
				KUBECONFIG: "/kubeconfig"
				KUBE_NAMESPACE: namespace
			}
			if (source & dagger.#Artifact) != _|_ {
				mount: "/source": source
			}
		}
	]
}
