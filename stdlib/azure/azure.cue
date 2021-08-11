// Azure base package
package azure

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
	"alpha.dagger.io/os"
)

// Default Azure CLI version
let defaultVersion = "2.27.1@sha256:1e117183100c9fce099ebdc189d73e506e7b02d2b73d767d3fc07caee72f9fb1"

//Azure Config shared by all Azure packages
#Config: {
	// AZURE tenant id
	tenantId: dagger.#Secret & dagger.#Input
	// AZURE subscription id
	subscriptionId: dagger.#Secret & dagger.#Input
	// AZURE app id for the service principal used
	appId: dagger.#Secret & dagger.#Input
	// AZURE password for the service principal used
	password: dagger.#Secret & dagger.#Input
}

// Azure Cli to be used by all Azure packages
#CLI: {
	// Azure Config
	config: #Config

	// Azure CLI version to install
	version: string | *defaultVersion

	// Container image
	os.#Container & {
		image: docker.#Pull & {
			from: "mcr.microsoft.com/azure-cli:\(version)"
		}
		
		always: true

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
