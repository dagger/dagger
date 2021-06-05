package compose

import (
	"strconv"

	"dagger.io/dagger"
	"dagger.io/dagger/op"
)

#CleanupCompose: {
	// docker-compose up context
	context: dagger.#Artifact

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
				export DOCKER_HOST="ssh://$DOCKER_USERNAME@$DOCKER_HOSTNAME:$DOCKER_PORT"

				# Start ssh agent
				eval $(ssh-agent) > /dev/null
				ssh-add /key > /dev/null

				# Down
				cd /context
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
				DOCKER_HOSTNAME: ssh.host
				DOCKER_USERNAME: ssh.user
				DOCKER_PORT:     strconv.FormatInt(ssh.port, 10)
			}
			mount: {
				if ssh.key != _|_ {
					"/key": secret: ssh.key
				}
			}
		},
	]
}
