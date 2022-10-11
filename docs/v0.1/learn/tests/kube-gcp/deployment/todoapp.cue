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

// GCR registry to push images to
registry: string & dagger.#Input
tag:      "test-gcr"

// source of Kube config file. 
// set with `dagger input dir manifest ./k8s -e kube`
manifest: dagger.#Artifact & dagger.#Input

// Declarative name
todoApp: {
	// Build an image from the project repository
	image: docker.#Build & {
		source: repository
	}

	// Push the image to a remote registry
	remoteImage: docker.#Push & {
		target: "\(registry):\(tag)"
		source: image
		auth: {
			username: gcrCreds.username
			secret:   gcrCreds.secret
		}
	}

	// Update the image of the deployment to the deployed image
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

	// Value created for generic reference of `kubeconfig` in `todoapp.cue`
	kubeSrc: kubernetes.#Resources & {
		"kubeconfig": kubeconfig
		source:       kustomization
	}
}
