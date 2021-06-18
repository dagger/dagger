package main

import (
	"dagger.io/dagger/op"
	"dagger.io/kubernetes"
)

#VerifyApply: {
	podname: string

	namespace: string

	// Verify that pod exist
	#GetPods:
		"""
        kubectl get pods --namespace "$KUBE_NAMESPACE" \( podname )
    """

	// Clear that pod for future test
	#DeletePods:
		"""
        kubectl delete pods --namespace "$KUBE_NAMESPACE" \( podname )
    """

	#up: [
		op.#Load & {
			from: kubernetes.#Kubectl
		},

		op.#WriteFile & {
			dest:    "/kubeconfig"
			content: TestKubeconfig
			mode:    0o600
		},

		op.#WriteFile & {
			dest:    "/getPods.sh"
			content: #GetPods
		},

		// Check pods
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
			env: {
				KUBECONFIG:     "/kubeconfig"
				KUBE_NAMESPACE: namespace
			}
		},

		op.#WriteFile & {
			dest:    "/deletePods.sh"
			content: #DeletePods
		},

		op.#Exec & {
			always: true
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/deletePods.sh",
			]
			env: {
				KUBECONFIG:     "/kubeconfig"
				KUBE_NAMESPACE: namespace
			}
		},
	]
}
