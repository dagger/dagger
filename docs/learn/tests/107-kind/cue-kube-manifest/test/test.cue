package main

import (
	"alpha.dagger.io/git"
	"alpha.dagger.io/kubernetes"
	"alpha.dagger.io/dagger/op"
)

repository: git.#Repository & {
	remote: "https://github.com/dagger/examples.git"
	ref:    "main"
	subdir: "todoapp"
}

test: #up: [
	op.#Load & {
		from: kubernetes.#Kubectl
	},

	// Write kubeconfig
	op.#WriteFile & {
		dest:    "/kubeconfig"
		content: todoApp.kubeSrc.kubeconfig
		mode:    0o600
	},

	// Check deployment
	op.#Exec & {
		always: true
		args: ["/bin/bash", "-c", "kubectl describe deployment todoapp | grep 'True'"]
		env: KUBECONFIG: "/kubeconfig"
	},
]

clean: #up: [
	op.#Load & {
		from: test
	},

	op.#WriteFile & {
		content: todoApp.kubeSrc.manifest
		dest:    "/resources"
	},

	// Clean deployment
	op.#Exec & {
		always: true
		args: ["/bin/bash", "-c", "kubectl delete -f /resources"]
		env: KUBECONFIG: "/kubeconfig"
	},
]
