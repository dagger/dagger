package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
)

// Build a Docker image from source, using included Dockerfile
#Build: {
	source: dagger.#Artifact

	#up: [
		op.#DockerBuild & {
			context: source
		},
	]

}

// Build a Docker image from source, using included Dockerfile
// FIXME: DEPRECATED by #Build
#ImageFromSource: #Build

// Fetch an image from a remote registry
#ImageFromRegistry: {
	ref: string

	#up: [
		op.#FetchContainer & {
			"ref": ref
		},
	]
}

// Build a Docker image from the provided Dockerfile contents
#ImageFromDockerfile: {
	dockerfile: string
	context:    dagger.#Artifact

	#up: [
		op.#DockerBuild & {
			"context":    context
			"dockerfile": dockerfile
		},
	]
}
