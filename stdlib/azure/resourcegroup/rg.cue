package resourcegroup

import (
	"alpha.dagger.io/os"
	"alpha.dagger.io/azure"
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
		// Path of the shell to execute
		shell: path: "/bin/bash"

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
