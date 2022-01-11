package secretmanager

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/gcp"
	"alpha.dagger.io/gcp/secretmanager"
	"alpha.dagger.io/os"
)

TestConfig: gcpConfig: gcp.#Config

TestSecrets: {
	secret: secretmanager.#Secrets & {
		config: TestConfig.gcpConfig
		secrets: {
			databasePassword: dagger.#Secret @dagger(input)
		}
	}

	if len(secret.references) > 0 {
		cleanup: os.#Container & {
			image: gcp.#GCloud & {
				config: TestConfig.gcpConfig
			}
			shell: path: "/bin/bash"
			always: true

			command: #"""
					gcloud -q secrets delete databasePassword
				"""#
		}
	}
}
