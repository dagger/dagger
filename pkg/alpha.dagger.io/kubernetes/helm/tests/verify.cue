package helm

import (
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/kubernetes"
)

#VerifyHelm: {
	chartName: string

	namespace: string

	// Verify that pod exist
	#getHelmPods:
		"""
        kubectl get pods --namespace "$KUBE_NAMESPACE" | grep "\(chartName)"
    """

	#up: [
		op.#Load & {
			from: kubernetes.#Kubectl
		},

		op.#WriteFile & {
			dest:    "/getHelmPods.sh"
			content: #getHelmPods
		},

		op.#WriteFile & {
			dest:    "/kubeconfig"
			content: TestKubeconfig
			mode:    0o600
		},

		op.#Exec & {
			always: true
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/getHelmPods.sh",
			]
			env: {
				KUBECONFIG:     "/kubeconfig"
				KUBE_NAMESPACE: namespace
			}
		},
	]
}
