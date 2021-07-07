package main

import (
	"alpha.dagger.io/git"
	"alpha.dagger.io/kubernetes"
	"alpha.dagger.io/dagger/op"
)

manifest: git.#Repository & {
	remote: "https://github.com/dagger/examples.git"
	ref:    "main"
	subdir: "todoapp/k8s"
}

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
		content: todoApp.kubeconfig
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

	// Clean deployment
	op.#Exec & {
		always: true
		args: ["/bin/bash", "-c", "kubectl delete -f /resources"]
		env: KUBECONFIG: "/kubeconfig"
		mount: "/resources": from: todoApp.source
	},
]
