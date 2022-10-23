package samImage

import (
	"dagger.io/dagger/sdk/go/dagger"
	"universe.dagger.io/alpha/aws/sam"
)

dagger.#Plan & {
	_common: config: sam.#Config & {
		accessKey:    client.env.AWS_ACCESS_KEY_ID
		region:       client.env.AWS_REGION
		secretKey:    client.env.AWS_SECRET_ACCESS_KEY
		stackName:    client.env.AWS_STACK_NAME
		clientSocket: client.network."unix:///var/run/docker.sock".connect
	}

	client: {
		filesystem: "./": read: contents: dagger.#FS
		network: "unix:///var/run/docker.sock": connect: dagger.#Socket
		env: {
			AWS_ACCESS_KEY_ID:     string
			AWS_REGION:            string
			AWS_SECRET_ACCESS_KEY: dagger.#Secret
			AWS_STACK_NAME:        string
		}
	}

	actions: {
		build: sam.#Build & _common & {
			fileTree: client.filesystem."./".read.contents
		}
		deploy: sam.#Deployment & _common & {
			input: build.output
		}
	}
}
