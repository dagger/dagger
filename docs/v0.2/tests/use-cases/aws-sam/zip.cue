package samZip

import (
	"dagger.io/dagger"
	"universe.dagger.io/alpha/aws/sam"
)

dagger.#Plan & {
	_common: config: sam.#Config & {
		accessKey: client.env.AWS_ACCESS_KEY_ID
		region:    client.env.AWS_REGION
		bucket:    client.env.AWS_S3_BUCKET
		secretKey: client.env.AWS_SECRET_ACCESS_KEY
		stackName: client.env.AWS_STACK_NAME
	}

	client: {
		filesystem: "./": read: contents: dagger.#FS
		env: {
			AWS_ACCESS_KEY_ID:     string
			AWS_REGION:            string
			AWS_S3_BUCKET:         string
			AWS_SECRET_ACCESS_KEY: dagger.#Secret
			AWS_STACK_NAME:        string
		}
	}

	actions: {
		build: sam.#Package & _common & {
			fileTree: client.filesystem."./".read.contents
		}
		deploy: sam.#DeployZip & _common & {
			input: build.output
		}
	}
}
