package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
)

// Build a Docker image from source, using included Dockerfile
#Build: {
	source: dagger.#Artifact @dagger(input)

	#up: [
		op.#DockerBuild & {
			context: source
		},
	]

}

// Pull a docker container
#Pull: {
	// Remote ref (example: "index.docker.io/alpine:latest")
	from: string @dagger(input)

	#up: [
		op.#FetchContainer & {ref: from},
	]
}

// Push a docker image
#Push: {
	// Remote ref (example: "index.docker.io/alpine:latest")
	ref: string @dagger(input)

	// Image
	source: dagger.#Artifact @dagger(input)

	#up: [
		op.#Load & {from:           source},
		op.#PushContainer & {"ref": ref},
	]
}

// FIXME: #Run

// Build a Docker image from the provided Dockerfile contents
// FIXME: incorporate into #Build
#ImageFromDockerfile: {
	dockerfile: string @dagger(input)
	context:    dagger.#Artifact @dagger(input)

	#up: [
		op.#DockerBuild & {
			"context":    context
			"dockerfile": dockerfile
		},
	]
}
