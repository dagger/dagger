// Azure base package
package login

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

// Default Azure CLI version
#DefaultVersion: "3.0"

#Config: {
	subscriptionId: dagger.#Secret
	version: *#DefaultVersion | string
}

#Image: {

	config: #Config

	docker.#Build & {
		steps: [
			docker.#Pull & {
				source: "barbo69/dagger-azure-cli:\(config.version)"
			},
			docker.#Run & {
				command: {
					name: "az"
					args: ["login"]
				}
			},

			docker.#Run & {
				env: AZ_SUB_ID_TOKEN: config.subscriptionId
				command: {
					name: "sh"
					flags: "-c": "az account set -s $AZ_SUB_ID_TOKEN"
				}
			}
		]
	}
}
