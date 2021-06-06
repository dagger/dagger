package compose

import (
	"strconv"

	"dagger.io/alpine"
	"dagger.io/dagger/op"
	"dagger.io/dagger"
)

#VerifyCompose: {
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

	port: int | *8080

	#code: #"""
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
				ssh -i /key -o "UserKnownHostsFile "$HOME"/.ssh/known_hosts" -o "StrictHostKeyChecking accept-new" -p "$REMOTE_PORT" "$REMOTE_USERNAME"@"$REMOTE_HOSTNAME" /bin/true > /dev/null 2>&1
			fi

			
			sleep 2 ; ssh -i /key -p "$REMOTE_PORT" "$REMOTE_USERNAME"@"$REMOTE_HOSTNAME" curl -L localhost:"$CONTAINER_PORT"
		"""#

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				package: {
					bash:             true
					curl:             true
					"openssh-client": true
				}
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
				CONTAINER_PORT:  strconv.FormatInt(port, 10)
				REMOTE_HOSTNAME: ssh.host
				REMOTE_USERNAME: ssh.user
				REMOTE_PORT:     strconv.FormatInt(ssh.port, 10)
				if ssh.keyPassphrase != _|_ {
					SSH_ASKPASS: "/get_passphrase"
					DISPLAY:     "1"
				}
				if ssh.fingerprint != _|_ {
					FINGERPRINT: ssh.fingerprint
				}
			}
			mount: {
				"/key": secret: ssh.key
				if ssh.keyPassphrase != _|_ {
					"/passphrase": secret: ssh.keyPassphrase
				}
			}
		},
	]
}
