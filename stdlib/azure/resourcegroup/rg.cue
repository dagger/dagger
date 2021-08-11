package resourcegroup

import (
	"alpha.dagger.io/azure"
	"alpha.dagger.io/os"
)

// Create a resource group
#ResourceGroup: {
	// Azure Config
	config: azure.#Config

	// ResourceGroup name
	rgName: string @dagger(input)

	// ResourceGroup location
	rgLocation: string @dagger(input)

	// Container image
	ctr: os.#Container & {
		image: azure.#CLI & {
			"config": config
		}
		always: true

		command: """
			az group create -l "\(rgLocation)" -n "\(rgName)"
			az group show -n "\(rgName)" --query "id" -o json | jq -r . | tr -d "\n" > /resourceGroupId
			"""
	}

	// Resource Id
	id: {
		os.#File & {
				from: ctr
				path: "/resourceGroupId"
			}
	}.contents @dagger(output)
}
