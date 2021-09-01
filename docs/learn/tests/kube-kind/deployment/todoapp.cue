package main

import (
	"encoding/yaml"

	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
	"alpha.dagger.io/kubernetes"
	"alpha.dagger.io/kubernetes/kustomize"
)

// input: source code repository, must contain a Dockerfile
// set with `dagger input dir repository . -e kube`
repository: dagger.#Artifact & dagger.#Input

// Registry to push images to
registry: string & dagger.#Input
tag:      "test-kind"

// input: kubernetes objects directory to deploy to
// set with `dagger input dir manifest ./k8s -e kube`
manifest: dagger.#Artifact & dagger.#Input

// Todoapp deployment pipeline
todoApp: {
	// Build the image from repositoru artifact
	image: docker.#Build & {
		source: repository
	}

	// Push image to registry
	remoteImage: docker.#Push & {
		target: "\(registry):\(tag)"
		source: image
	}

	// Update the image from manifest to use the deployed one
	kustomization: kustomize.#Kustomize & {
		source: manifest

		// Convert CUE to YAML.
		kustomization: yaml.Marshal({
			resources: ["deployment.yaml", "service.yaml"]

			images: [{
				name:    "public.ecr.aws/j7f8d3t2/todoapp"
				newName: remoteImage.ref
			}]
		})
	}

	// Deploy the customized manifest to a kubernetes cluster
	kubeSrc: kubernetes.#Resources & {
		"kubeconfig": kubeconfig
		source:       kustomization
	}
}
