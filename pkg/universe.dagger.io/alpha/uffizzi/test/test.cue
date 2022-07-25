package uffizzi

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/alpha/uffizzi"
)

dagger.#Plan & {

	client: filesystem: "./data": read: {
		contents: dagger.#FS
		include: ["*", "./..."]
	}

	client: commands: sops: {
		name: "sops"
		args: ["-d", "../../../../../tests/secrets_sops.yaml"]
		stdout: dagger.#Secret
	}

	actions: test: {
		secrets: core.#DecodeSecret & {
			input:  client.commands.sops.stdout
			format: "yaml"
		}

		_data: {
			load: core.#Source & {
				path: "./data"
				include: ["*.yaml"]
			}
			contents: load.output
		}

		createEnvironment: uffizzi.#CreateEnvironment & {
			uffizzi_user:       "vibhav.bobade+dagger@uffizzi.com"
			uffizzi_password:   secrets.output.UFFIZZI_PASSWORD.contents
			uffizzi_project:    "dagger-2zkd"
			uffizzi_server:     "https://app.uffizzi.com"
			dockerhub_username: "daggertest"
			dockerhub_password: secrets.output.DOCKERHUB_TOKEN.contents
			source:             _data.contents

			docker_compose_file: "docker-compose.yaml"
		}
	}
}
