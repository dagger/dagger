package docker

import (
	"strconv"

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

	#code: #"""
		if [ -n "$DOCKER_HOSTNAME" ]; then
			export DOCKER_HOST="ssh://$DOCKER_USERNAME@$DOCKER_HOSTNAME:$DOCKER_PORT"

			# Start ssh-agent
			eval $(ssh-agent) > /dev/null

			# Add key
			if [ -f "/key" ]; then
				message="$(ssh-keygen -y -f /key < /dev/null 2>&1)" || {
					>&2 echo "$message"
					exit 1
				}

				ssh-add /key > /dev/null
				if [ "$?" != 0 ]; then
					exit 1
				fi
			fi

			if [[ ! -z $FINGERPRINT ]]; then
				mkdir -p "$HOME"/.ssh

				# Add user's fingerprint to known hosts
				echo "$FINGERPRINT" >> "$HOME"/.ssh/known_hosts
			else
				# Add host to known hosts
				ssh -i /key -o "UserKnownHostsFile "$HOME"/.ssh/known_hosts" -o "StrictHostKeyChecking accept-new" -p "$DOCKER_PORT" "$DOCKER_USERNAME"@"$DOCKER_HOSTNAME" /bin/true > /dev/null 2>&1
			fi
		fi


		# Run detach container
		OPTS=""

		if [ ! -z "$CONTAINER_NAME" ]; then
			OPTS="$OPTS --name $CONTAINER_NAME"
		fi

		docker container run -d $OPTS "$IMAGE_REF"
		"""#

	#up: [
		op.#Load & {from: #Client},

		if registry != _|_ {
			op.#DockerLogin & {registry}
		},

		if ssh.keyPassphrase != _|_ {
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
				IMAGE_REF: ref
				if ssh != _|_ {
					DOCKER_HOSTNAME: ssh.host
					DOCKER_USERNAME: ssh.user
					DOCKER_PORT:     strconv.FormatInt(ssh.port, 10)
					if ssh.keyPassphrase != _|_ {
						SSH_ASKPASS: "/get_passphrase"
						DISPLAY:     "1"
					}
					if ssh.fingerprint != _|_ {
						FINGERPRINT: ssh.fingerprint
					}
				}
				if name != _|_ {
					CONTAINER_NAME: name
				}
			}
			mount: {
				if ssh.key != _|_ {
					"/key": secret: ssh.key
				}
				if ssh.keyPassphrase != _|_ {
					"/passphrase": secret: ssh.keyPassphrase
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
