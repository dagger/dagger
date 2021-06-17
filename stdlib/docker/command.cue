package docker

import (
	"strconv"

	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
)

// A container image that can run any docker command
#Command: {
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

	// Command to execute
	command: string @dagger(input)

	// Environment variables shared by all commands
	env: {
		[string]: string @dagger(input)
	}

	// Mount content from other artifacts
	mount: {
		[string]: {
			from: dagger.#Artifact
		} | {
			secret: dagger.#Secret
		} @dagger(input)
	}

	// Mount persistent cache directories
	cache: {
		[string]: true @dagger(input)
	}

	// Mount temporary directories
	tmpfs: {
		[string]: true @dagger(input)
	}

	// Additional packages to install
	package: {
		[string]: true | false | string @dagger(input)
	}

	// Image registries
	registries: [...{
		target?:  string
		username: string
		secret:   dagger.#Secret
	}] @dagger(input)

	// Copy contents from other artifacts
	copy: [string]: from: dagger.#Artifact

	// Write file in the container
	files: [string]: string

	// Setup docker client and then execute the user command
	#code: #"""
		# Setup ssh
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
				ssh -i /key -o "UserKnownHostsFile "$HOME"/.ssh/known_hosts" -o "StrictHostKeyChecking accept-new" -p "$DOCKER_PORT" "$DOCKER_USERNAME"@"$DOCKER_HOSTNAME" /bin/true > /dev/null 2>&1
			fi
		fi

		# Execute entrypoint
		/bin/bash /entrypoint.sh
		"""#

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				"package": {
					package
					bash:             true
					"openssh-client": true
					"docker-cli":     true
				}
			}
		},

		for registry in registries {
			op.#Exec & {
				args: ["/bin/bash", "-c", #"""
						echo "$TARGER_HOST" | docker login --username "$DOCKER_USERNAME" --password-stdin "$(cat /password)" 
					"""#,
				]
				env: {
					TARGET_HOST:     registry.target
					DOCKER_USERNAME: registry.username
				}
				mount: "/password": secret: registry.password
			}
		},

		for dest, content in files {
			op.#WriteFile & {
				"content": content
				"dest":    dest
			}
		},

		for dest, src in copy {
			op.#Copy & {
				from:   src.from
				"dest": dest
			}
		},

		if ssh.keyPassphrase != _|_ {
			op.#WriteFile & {
				content: #"""
					#!/bin/bash
					cat /keyPassphrase
					"""#
				dest: "/get_keyPassphrase"
				mode: 0o500
			}
		},

		// Write wrapper
		op.#WriteFile & {
			content: #code
			dest:    "/setup.sh"
		},

		// Write entrypoint
		op.#WriteFile & {
			content: command
			dest:    "/entrypoint.sh"
		},

		op.#Exec & {
			always: true
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/setup.sh",
			]
			"env": {
				env
				if ssh != _|_ {
					DOCKER_HOSTNAME: ssh.host
					DOCKER_USERNAME: ssh.user
					DOCKER_PORT:     strconv.FormatInt(ssh.port, 10)
					if ssh.keyPassphrase != _|_ {
						SSH_ASKPASS: "/get_keyPassphrase"
						DISPLAY:     "1"
					}
					if ssh.fingerprint != _|_ {
						FINGERPRINT: ssh.fingerprint
					}
				}
			}
			"mount": {
				if ssh == _|_ {
					"/var/run/docker.sock": from: "docker.sock"
				}
				if ssh != _|_ {
					if ssh.key != _|_ {
						"/key": secret: ssh.key
					}
					if ssh.keyPassphrase != _|_ {
						"/keyPassphrase": secret: ssh.keyPassphrase
					}
				}
				for dest, o in mount {
					"\(dest)": o
				}
				for dest, _ in cache {
					"\(dest)": "cache"
				}
				for dest, _ in tmpfs {
					"\(dest)": "tmpfs"
				}
			}
		},
	]
}
