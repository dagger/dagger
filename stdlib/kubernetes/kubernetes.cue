// Kubernetes client operations
package kubernetes

import (
	"dagger.io/dagger/op"
	"dagger.io/dagger"
	"dagger.io/alpine"
)

// Kubectl client
#Kubectl: {

	// Kubectl version
	version: *"v1.19.9" | string

	#code: #"""
		[ -e /usr/local/bin/kubectl ] || {
			curl -sfL https://dl.k8s.io/${KUBECTL_VERSION}/bin/linux/amd64/kubectl -o /usr/local/bin/kubectl \
			&& chmod +x /usr/local/bin/kubectl
		}
		"""#

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				package: bash: "=~5.1"
				package: jq:   "=~1.6"
				package: curl: true
			}
		},
		op.#WriteFile & {
			dest:    "/entrypoint.sh"
			content: #code
		},
		op.#Exec & {
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/entrypoint.sh",
			]
			env: KUBECTL_VERSION: version
		},
	]
}

// Apply Kubernetes resources
#Resources: {

	// Kubernetes config to deploy
	source?: dagger.#Artifact @dagger(input)

	// Kubernetes manifest to deploy inlined in a string
	manifest?: string @dagger(input)

	// Kubernetes Namespace to deploy to
	namespace: *"default" | string @dagger(input)

	// Version of kubectl client
	version: *"v1.19.9" | string @dagger(input)

	// Kube config file
	kubeconfig: string @dagger(input)

	#code: #"""
		kubectl create namespace "$KUBE_NAMESPACE"  > /dev/null 2>&1 || true
		kubectl --namespace "$KUBE_NAMESPACE" apply -R -f /source
		"""#

	#up: [
		op.#Load & {
			from: #Kubectl & {"version": version}
		},
		op.#WriteFile & {
			dest:    "/entrypoint.sh"
			content: #code
		},
		op.#WriteFile & {
			dest:    "/kubeconfig"
			content: kubeconfig
			mode:    0o600
		},
		if manifest != _|_ {
			op.#WriteFile & {
				dest:    "/source"
				content: manifest
			}
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
				KUBECONFIG:     "/kubeconfig"
				KUBE_NAMESPACE: namespace
			}
			if manifest == _|_ {
				mount: "/source": from: source
			}
		},
	]
}
