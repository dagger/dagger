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

#Run: {
	// Connect to a remote SSH server
	ssh: {
		// ssh host
		host: string @dagger(input)

		// ssh user
		user: string @dagger(input)

		// ssh port
		port: *22 | int @dagger(input)

		// private key
		key: dagger.#Secret @dagger(input)

		// fingerprint
		fingerprint?: string @dagger(input)

		// ssh key passphrase
		keyPassphrase?: dagger.#Secret @dagger(input)
	}

	// Image reference (e.g: nginx:alpine)
	ref: string @dagger(input)

	// Container name
	name?: string @dagger(input)

	// Image registry
	registry?: {
		target:   string
		username: string
		secret:   dagger.#Secret
	} @dagger(input)

	#command: #"""
		# Run detach container
		OPTS=""

		if [ ! -z "$CONTAINER_NAME" ]; then
			OPTS="$OPTS --name $CONTAINER_NAME"
		fi

		docker container run -d $OPTS "$IMAGE_REF"
		"""#

	run: #Command & {
		"ssh":   ssh
		command: #command
		env: {
			IMAGE_REF: ref
			if name != _|_ {
				CONTAINER_NAME: name
			}
		}
	}
}

// Build a Docker image from the provided Dockerfile contents
// FIXME: incorporate into #Build
#ImageFromDockerfile: {
	dockerfile: string           @dagger(input)
	context:    dagger.#Artifact @dagger(input)

	#up: [
		op.#DockerBuild & {
			"context":    context
			"dockerfile": dockerfile
		},
	]
}
