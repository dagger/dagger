package kubernetes

import (
	"dagger.io/dagger/op"
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

// Apply a Kubernetes configuration
#Apply: {

	// Kubernetes config to deploy
	source: dagger.#Artifact @dagger(input)

	// Kubernetes config to deploy inlined in a string
	sourceInline?: string @dagger(input)

	// Kubernetes Namespace to deploy to
	namespace: string @dagger(input)

	// Version of kubectl client
	version: *"v1.19.9" | string @dagger(input)

	// Kube config file
	kubeconfig: dagger.#Secret @dagger(input)

	#code: #"""
		kubectl create namespace "$KUBE_NAMESPACE" || true
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
		if sourceInline != _|_ {
			op.#WriteFile & {
				dest:    "/source"
				content: sourceInline
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
			if sourceInline == _|_ {
				mount: "/source": from: source
			}
		},
	]
}
