package main

import (
	"dagger.io/dagger"
	"dagger.io/docker"
)

// Container source code (must include a Dockerfile)
source: dagger.#Artifact

// Container image
image: docker.#Build & {
	"source": source
}
