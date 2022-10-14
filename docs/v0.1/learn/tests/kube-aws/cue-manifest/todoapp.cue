package main

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
	"alpha.dagger.io/kubernetes"
)

// input: source code repository, must contain a Dockerfile
// set with `dagger input dir repository . -e kube`
repository: dagger.#Artifact & dagger.#Input

// ECR registry to push images to
registry: string & dagger.#Input
tag:      "test-ecr"

// Todoapp deployment pipeline
todoApp: {
	// Build the image from repository artifact
	image: docker.#Build & {
		source: repository
	}

	// Push image to registry
	remoteImage: docker.#Push & {
		target: "\(registry):\(tag)"
		source: image
		auth: {
			username: ecrCreds.username
			secret:   ecrCreds.secret
		}
	}

	// Generate deployment manifest
	deployment: #AppManifest & {
		name:  "todoapp"
		image: remoteImage.ref
	}

	// Deploy the customized manifest to a kubernetes cluster
	kubeSrc: kubernetes.#Resources & {
		"kubeconfig": kubeconfig
		manifest:     deployment.manifest
	}
}
