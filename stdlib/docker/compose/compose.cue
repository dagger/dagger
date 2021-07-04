// Docker-compose operations
package compose

import (
	"strconv"

	"alpha.dagger.io/dagger"
	"alpha.dagger.io/ssh"
	"alpha.dagger.io/docker"
)

#App: {
	sshConfig?: {
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
	source?:      dagger.#Artifact @dagger(input)
	composeFile?: string           @dagger(input)

	// App name (use as COMPOSE_PROJECT_NAME)
	name: *"source" | string @dagger(input)

	// Volumes used by compose file
	volumes: {[string]: dagger.#Artifact & dagger.#Input} | *null

	// Secrets volumes used by compose file
	secrets: {[string]: dagger.#Secret & dagger.#Input} | *null

	// Image registries
	registries: [...{
		target?:  string
		username: string
		secret:   dagger.#Secret
	}] @dagger(input)

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
			if [ -f docker-compose.yml ]; then
				rm "$SOURCE_DIR"
				cp docker-compose.yml "$SOURCE_DIR"/docker-compose.yml
			fi
			cd "$SOURCE_DIR"
		fi

		docker-compose build
		docker-compose up -d
		"""#

	// FIXME Create a dependency on files to do docker-compose up
	// after uploading files.
	// It's actually not possible because `files` isn't in `run` scope
	if volumes != null || secrets != null {
		files: ssh.#Files & {
			"sshConfig": sshConfig
			files:       volumes
			"secrets":   secrets
		}
	}

	run: docker.#Command & {
		"sshConfig": sshConfig
		command:     #code
		package: "docker-compose": true
		"registries": registries
		if source != _|_ {
			copy: "/source": from: source
		}
		if composeFile != _|_ {
			files: "/docker-compose.yml": composeFile
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
