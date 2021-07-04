package ssh

import (
	"strconv"

	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
)

// Upload files or secrets to remote host
#Files: {
	sshConfig: {
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

	// Files to upload to remote host
	files: {
		[string]: dagger.#Artifact @dagger(input)
	}

	// Secrets to upload to remote host
	secrets: {
		[string]: dagger.#Secret @dagger(input)
	}

	#code: #"""
		# Start ssh-agent
		eval $(ssh-agent) > /dev/null

		# Add key
		if [ -f "/key" ]; then
			message="$(ssh-keygen -y -f /key < /dev/null 2>&1)" || {
				>&2 echo "$message"
				exit 1
			}

			# Save key
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

		# Upload directories to remote host
		rsync -e "ssh -i /key" -r "/inputs/" "$REMOTE_USERNAME@$REMOTE_HOSTNAME:/"
		"""#

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				package: {
					bash:             true
					"openssh-client": true
					rsync:            true
				}
			}
		},

		// Write entrypoint
		op.#WriteFile & {
			content: #code
			dest:    "/entrypoint.sh"
		},

		if sshConfig.keyPassphrase != _|_ {
			op.#WriteFile & {
				content: #"""
					#!/bin/bash
					cat /keyPassphrase
					"""#
				dest: "/get_keyPassphrase"
				mode: 0o500
			}
		},

		op.#Exec & {
			always: true
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/entrypoint.sh",
			]
			env: {
				REMOTE_HOSTNAME: sshConfig.host
				REMOTE_USERNAME: sshConfig.user
				REMOTE_PORT:     strconv.FormatInt(sshConfig.port, 10)
				if sshConfig.keyPassphrase != _|_ {
					SSH_ASKPASS: "/get_keyPassphrase"
					DISPLAY:     "1"
				}
				if sshConfig.fingerprint != _|_ {
					FINGERPRINT: sshConfig.fingerprint
				}
			}
			mount: {
				"/key": secret: sshConfig.key
				if sshConfig.keyPassphrase != _|_ {
					"/keyPassphrase": secret: sshConfig.keyPassphrase
				}
				for target, secret in secrets {
					"/inputs/\(target)": "secret": secret
				}
				for target, file in files {
					"/inputs/\(target)": from: file
				}
			}
		},
	]
}
