// Azure base package
package azure

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
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
	// Azure Config
	config: #Config

	#up: [
		op.#FetchContainer & {
			ref: "mcr.microsoft.com/azure-cli"
		},

		op.#Exec & {
			args: ["sh", "-c",
				#"""
					az login --service-principal -u "$(cat /run/secrets/appId)" -p "$(cat /run/secrets/password)" -t "$(cat /run/secrets/tenantId)"
					az account set -s "$(cat /run/secrets/subscriptionId)"
					"""#,
			]
			mount: "/run/secrets/appId": secret:          config.appId
			mount: "/run/secrets/password": secret:       config.password
			mount: "/run/secrets/tenantId": secret:       config.tenantId
			mount: "/run/secrets/subscriptionId": secret: config.subscriptionId
		},
	]
}
