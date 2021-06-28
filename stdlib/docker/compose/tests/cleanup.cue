package compose

import (
	"strconv"

	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
)

#CleanupCompose: {
	// docker-compose up context
	context: dagger.#Artifact

	// App name (use as COMPOSE_PROJECT_NAME)
	name: *"source" | string

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

	#code: #"""
				# Export host
				export DOCKER_HOST="unix://$(pwd)/docker.sock"

				# Start ssh agent
				eval $(ssh-agent) > /dev/null
				ssh-add /key > /dev/null

				ssh -i /key -o "StreamLocalBindUnlink=yes" -fNT -L "$(pwd)"/docker.sock:/var/run/docker.sock -p "$DOCKER_PORT" "$DOCKER_USERNAME"@"$DOCKER_HOSTNAME" || true

				# Down
				if [ -d /source ]; then
					cd /source
				fi

				docker-compose down -v
		"""#

	#up: [
		op.#Load & {from: context},

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
				DOCKER_HOSTNAME:      ssh.host
				DOCKER_USERNAME:      ssh.user
				DOCKER_PORT:          strconv.FormatInt(ssh.port, 10)
				COMPOSE_PROJECT_NAME: name
				if ssh.keyPassphrase != _|_ {
					SSH_ASKPASS: "/get_passphrase"
					DISPLAY:     "1"
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
