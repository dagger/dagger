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
	// Remote host
	host: string

	// Remote user
	user: *"root" | string

	// Ssh remote port
	port: *22 | int

	// Ssh private key
	key: dagger.#Artifact

	// Ssh passphrase
	passphrase?: string

	// Image reference (e.g: nginx:alpine)
	ref: string

	// Container name
	name?: string

	// Image registry
	registry?: {
		username: string
		secret:   dagger.#Secret
	}

	#code: #"""
			# Add host to known hosts
			ssh -i /key -o "UserKnownHostsFile $HOME/.ssh/known_hosts" -o "StrictHostKeyChecking accept-new" -p \#(port) \#(user)@\#(host) /bin/true &> /dev/null

			# Start ssh-agent
			eval $(ssh-agent) &> /dev/null

			# Add key
			ssh-add /key &> /dev/null

			# Run detach container
			OPTS=""

			if [ ! -z $CONTAINER_NAME ]; then
				OPTS="$OPTS --name $CONTAINER_NAME"
			fi

			docker container run -d $OPTS \#(ref)
	"""#

	#up: [
		op.#FetchContainer & {ref: "index.docker.io/docker:latest"},

		op.#WriteFile & {
			content: key
			dest:    "/key"
			mode:    0o600
		},

		if registry != _|_ {
			op.#DockerLogin & {registry}
		},

		if passphrase != _|_ {
			op.#WriteFile & {
				content: #"""
				#!/bin/sh
				echo '\#(passphrase)'
				"""#
				dest:    "/passphrase"
			}
		},

		op.#WriteFile & {
			content: #code
			dest:    "/entrypoint.sh"
		},

		op.#Exec & {
			always: true
			args: [
				"/bin/sh",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/entrypoint.sh",
			]
			env: {
				DOCKER_HOST: "ssh://\(user)@\(host):\(port)"
				if passphrase != _|_ {
					SSH_ASKPASS: "/passphrase"
					DISPLAY:     ""
				}
				if name != _|_ {
					CONTAINER_NAME: name
				}
			}
		},
	]
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
