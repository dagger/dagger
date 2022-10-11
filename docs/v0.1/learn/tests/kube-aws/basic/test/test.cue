package main

import (
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/kubernetes"
)

TestEks: {
	#_GetDeployment: """
			kubectl describe deployment todoapp | grep 'True'
		"""

	#_DeleteDeployment: """
			kubectl delete deployment todoapp
			kubectl delete service todoapp-service
		"""

	#up: [
		op.#Load & {
			from: kubernetes.#Kubectl
		},

		op.#WriteFile & {
			dest:    "/kubeconfig"
			content: todoApp.kubeconfig
		},

		op.#WriteFile & {
			dest:    "/getPods.sh"
			content: #_GetDeployment
		},

		op.#WriteFile & {
			dest:    "/deletePods.sh"
			content: #_DeleteDeployment
		},

		// Get pods
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

		// Delete pods
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
			env: KUBECONFIG: "/kubeconfig"
		},
	]
}
