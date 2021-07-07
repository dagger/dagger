package main

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/kubernetes"
)

// input: kubernetes objects directory to deploy to
// set with `dagger input dir manifest ./k8s -e kube`
manifest: dagger.#Artifact & dagger.#Input

// Deploy the manifest to a kubernetes cluster
todoApp: kubernetes.#Resources & {
	"kubeconfig": kubeconfig
	source:       manifest
}
