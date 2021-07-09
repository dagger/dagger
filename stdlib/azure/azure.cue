// Azure base package
package azure

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/os"
)

//Azure Config shared by all Azure packages
#Config: {
	// AZURE tenant id
	tenantId: dagger.#Secret @dagger(input)
	// AZURE subscription id
	subscriptionId: dagger.#Secret @dagger(input)
	// AZURE app id for the service principal used
	appId: dagger.#Secret @dagger(input)
	// AZURE password for the service principal used
	password: dagger.#Secret @dagger(input)
}

// Azure Cli to be used by all Azure packages
#CLI: {
	config: #Config

	// Container image
	ctr: os.#Container & {
		image: #up: [op.#FetchContainer & {
			ref: "mcr.microsoft.com/azure-cli"
		}]

		// Path of the shell to execute
		shell: path: "/bin/bash"

		command: """
			az login --service-principal -u "$(cat /run/secrets/appId)" -p "$(cat /run/secrets/password)" -t "$(cat /run/secrets/tenantId)"
			az account set -s "$(cat /run/secrets/subscriptionId)"
			"""

		secret: {
			"/run/secrets/appId":          config.appId
			"/run/secrets/password":       config.password
			"/run/secrets/tenantId":       config.tenantId
			"/run/secrets/subscriptionId": config.subscriptionId
		}
	}
}
