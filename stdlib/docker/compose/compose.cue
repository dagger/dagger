package compose

import (
	"strconv"
	"dagger.io/dagger"
	"dagger.io/dagger/op"
)

#Up: {
	ssh?: {
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

	// Accept either a contaxt, a docker-compose or both together
	context?:     dagger.#Artifact @dagger(input)
	composeFile?: string           @dagger(input)

	// Image registries
	registries: [...{
		target?:  string
		username: string
		secret:   dagger.#Secret
	}] @dagger(input)

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

			cd /context
			docker-compose build
			docker-compose up -d
		"""#

	#up: [
		op.#Load & {from: #Client},

		for registry in registries {
			op.#DockerLogin & {registry}
		},

		if context != _|_ {
			op.#Copy & {
				from: context
				dest: "/context/"
			}
		},

		if context == _|_ {
			op.#Mkdir & {
				path: "/context/"
			}
		},

		if composeFile != _|_ {
			op.#WriteFile & {
				content: composeFile
				dest:    "/context/docker-compose.yaml"
			}
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
			}
			mount: {
				if ssh == _|_ {
					"/var/run/docker.sock": "docker.sock"
				}
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
