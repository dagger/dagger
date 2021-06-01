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
	host: string @dagger(input)

	// Remote user
	user: string @dagger(input)

	// Ssh remote port
	port: *22 | int @dagger(input)

	// Ssh private key
	key: dagger.#Artifact @dagger(input)

	// User fingerprint
	fingerprint?: string @dagger(input)

	// Ssh passphrase
	passphrase?: string @dagger(input)

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

	#code: #"""
	export DOCKER_HOST="ssh://$DOCKER_USERNAME@$DOCKER_HOSTNAME:\#(port)"

	# Start ssh-agent
	eval $(ssh-agent) > /dev/null

	# Add key
	message="$(ssh-keygen -y -f /key < /dev/null 2>&1)" || {
		>&2 echo "$message"
		exit 1
	}

	ssh-add /key > /dev/null
	if [ "$?" != 0 ]; then
		exit 1
	fi

	if [[ ! -z $FINGERPRINT ]]; then
		mkdir -p "$HOME"/.ssh

		# Add user's fingerprint to known hosts
		echo "$FINGERPRINT" >> "$HOME"/.ssh/known_hosts
	else
		# Add host to known hosts
		ssh -i /key -o "UserKnownHostsFile "$HOME"/.ssh/known_hosts" -o "StrictHostKeyChecking accept-new" -p \#(port) "$DOCKER_USERNAME"@"$DOCKER_HOSTNAME" /bin/true > /dev/null 2>&1
	fi


	# Run detach container
	OPTS=""

	if [ ! -z "$CONTAINER_NAME" ]; then
		OPTS="$OPTS --name $CONTAINER_NAME"
	fi

	docker container run -d $OPTS \#(ref)
	"""#

	#up: [
		op.#Load & {from: #Client},

		op.#WriteFile & {
			content: key
			dest:    "/key"
			mode:    0o400
		},

		if registry != _|_ {
			op.#DockerLogin & {registry}
		},

		if passphrase != _|_ {
			op.#WriteFile & {
				content: passphrase
				dest:    "/passphrase"
				mode:    0o400
			}
		},

		if passphrase != _|_ {
			op.#WriteFile & {
				content: #"""
					#!/bin/bash
					cat /passphrase
					"""#
				dest: "/get_passphrase"
				mode: 0o500
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
				DOCKER_HOSTNAME: host
				DOCKER_USERNAME: user
				if passphrase != _|_ {
					SSH_ASKPASS: "/get_passphrase"
					DISPLAY:     "1"
				}
				if name != _|_ {
					CONTAINER_NAME: name
				}
				if fingerprint != _|_ {
					FINGERPRINT: fingerprint
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
