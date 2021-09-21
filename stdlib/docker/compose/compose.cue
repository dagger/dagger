// Docker-compose operations
package compose

import (
	"strconv"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
)

#App: {
	ssh?: {
		// ssh host
		host: string & dagger.#Input

		// ssh user
		user: string & dagger.#Input

		// ssh port
		port: *22 | int & dagger.#Input

		// private key
		key: dagger.#Secret & dagger.#Input

		// fingerprint
		fingerprint?: string & dagger.#Input

		// ssh key passphrase
		keyPassphrase?: dagger.#Secret & dagger.#Input
	}

	// Accept either a contaxt, a docker-compose or both together
	source?:      dagger.#Artifact & dagger.#Input
	composeFile?: string & dagger.#Input

	// App name (use as COMPOSE_PROJECT_NAME)
	name: *"source" | string & dagger.#Input

	// Image registries
	registries: [...{
		target?:  string
		username: string
		secret:   dagger.#Secret
	}] & dagger.#Input

	#code: #"""
		if [ -n "$DOCKER_HOSTNAME" ]; then
			ssh -i /key -fNT -o "StreamLocalBindUnlink=yes" -L "$(pwd)"/docker.sock:/var/run/docker.sock -p "$DOCKER_PORT" "$DOCKER_USERNAME"@"$DOCKER_HOSTNAME"
			export DOCKER_HOST="unix://$(pwd)/docker.sock"
		fi

		# Extend session duration
		echo "Host *\nServerAliveInterval 240" >> "$HOME"/.ssh/config
		chmod 600 "$HOME"/.ssh/config

		# Move compose
		if [ -d "$SOURCE_DIR" ]; then
			if [ -f docker-compose.yaml ]; then
				cp docker-compose.yaml "$SOURCE_DIR"/docker-compose.yaml
			fi
			cd "$SOURCE_DIR"
		fi

		docker-compose build
		docker-compose up -d
		"""#

	run: docker.#Command & {
		"ssh":   ssh
		command: #code
		package: "docker-compose": true
		"registries": registries
		if source != _|_ {
			copy: "/source": from: source
		}
		if composeFile != _|_ {
			files: "/docker-compose.yaml": composeFile
		}
		env: {
			COMPOSE_HTTP_TIMEOUT: strconv.FormatInt(200, 10)
			COMPOSE_PROJECT_NAME: name
			if source != _|_ {
				SOURCE_DIR: "source"
			}
		}
	}
}
