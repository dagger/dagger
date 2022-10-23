package samImageGitlabCI

import (
	"dagger.io/dagger/sdk/go/dagger"
	"universe.dagger.io/alpha/aws/sam"
)

dagger.#Plan & {
	_common: config: sam.#Config & {
		ciKey:     actions.ciKey
		accessKey: client.env.AWS_ACCESS_KEY_ID
		region:    client.env.AWS_REGION
		secretKey: client.env.AWS_SECRET_ACCESS_KEY
		stackName: client.env.AWS_STACK_NAME
		if (client.env.DOCKER_PORT_2376_TCP != _|_) {
			host: client.env.DOCKER_PORT_2376_TCP
		}
		if (actions.ciKey != null) {
			certs: client.filesystem."/certs/client".read.contents
		}
		clientSocket: client.network."unix:///var/run/docker.sock".connect
	}

	client: {
		filesystem: {
			"./": read: contents: dagger.#FS
			if actions.ciKey != null {
				"/certs/client": read: contents: dagger.#FS
			}
		}

		if actions.ciKey == null {
			network: "unix:///var/run/docker.sock": connect: dagger.#Socket
		}

		env: {
			AWS_ACCESS_KEY_ID:     string
			AWS_REGION:            string
			AWS_SECRET_ACCESS_KEY: dagger.#Secret
			AWS_STACK_NAME:        string
			DOCKER_PORT_2376_TCP?: string
		}
	}

	actions: {
		ciKey: *null | string
		build: sam.#Build & _common & {
			fileTree: client.filesystem."./".read.contents
		}
		deploy: sam.#Deployment & _common & {
			input: build.output
		}
	}
}
