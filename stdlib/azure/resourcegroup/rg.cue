package resourcegroup

import (
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/azure"
)

// Create a new resource group.
#ResourceGroup: {

	// Azure Config
	config: azure.#Config

	// ResourceGroup name
	rgName: string @dagger(input)

	// ResourceGroup location
	rgLocation: string @dagger(input)

	id: {
		string

		#up: [
			op.#Load & {
				from: azure.#CLI & {
					"config": config
				}
			},

			op.#Exec & {
				args: [
					"/bin/bash",
					"--noprofile",
					"--norc",
					"-eo",
					"pipefail",
					"-c",
					#"""
							az group create -l "$AZURE_DEFAULTS_LOCATION" -n "$AZURE_DEFAULTS_GROUP"
							az group show -n "$AZURE_DEFAULTS_GROUP" --query "id" > /resourceGroupId
						"""#,
				]
				env: {
					AZURE_DEFAULTS_LOCATION: rgLocation
					AZURE_DEFAULTS_GROUP:    rgName
				}
			},

			op.#Export & {
				source: "/resourceGroupId"
				format: "string"
			},
		]
	} @dagger(output)
}
