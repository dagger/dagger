package resourcegroup

import (
	"alpha.dagger.io/azure"
	"alpha.dagger.io/os"
	"alpha.dagger.io/dagger"
)

// Create a resource group
#ResourceGroup: {
	// Azure Config
	config: azure.#Config

	// ResourceGroup name
	rgName: string & dagger.#Input

	// ResourceGroup location
	rgLocation: string & dagger.#Input

	// ResourceGroup Id
	id: string & dagger.#Output

	// Container image
	ctr: os.#Container & {
		image: azure.#CLI & {
			"config": config
		}
		always: true

		command: """
			az group create -l "$AZURE_DEFAULTS_LOCATION" -n "$AZURE_DEFAULTS_GROUP"
			az group show -n "$AZURE_DEFAULTS_GROUP" --query "id" -o json | jq -r . | tr -d "\n" > /resourceGroupId
			"""

		env: {
			AZURE_DEFAULTS_GROUP: rgName
			AZURE_DEFAULTS_LOCATION: rgLocation
		}
	}

	// Resource Id
	id: ({
		os.#File & {
				from: ctr
				path: "/resourceGroupId"
			}
	}).contents
}
