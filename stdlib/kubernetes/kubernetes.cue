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
				package: bash: "=5.1.0-r0"
				package: jq:   "=1.6-r1"
				package: curl: "=7.74.0-r1"
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
	source: dagger.#Artifact

	// Kubernetes config to deploy inlined in a string
	sourceInline?: string

	// Kubernetes Namespace to deploy to
	namespace: string

	// Version of kubectl client
	version: *"v1.19.9" | string

	// Kube config file
	kubeconfig: dagger.#Secret

	#code: #"""
		kubectl create namespace "$KUBE_NAMESPACE" || true
		ls -la /source
		cat /source
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
				content: source
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
