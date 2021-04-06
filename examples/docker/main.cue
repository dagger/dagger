package main

import (
	"dagger.io/dagger"
	"dagger.io/docker"
)

// Container source code (must include a Dockerfile)
source: dagger.#Artifact

// Container image
container: docker.#ImageFromSource & {
	"source": source
}
