package storage

import (
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/azure"
)

// Create a storage account
#StorageAccount: {
	// Azure Config
	config: azure.#Config

	// ResourceGroup name
	rgName: string @dagger(input)

	// StorageAccount name
	accountName: string @dagger(input)

	// StorageAccount location
	accountLocation: string @dagger(input)

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
							az storage account create -l "$AZURE_DEFAULTS_LOCATION" -g "$AZURE_DEFAULTS_GROUP" -n "$AZURE_STORAGE_ACCOUNT"
							az storage account show -n "$AZURE_STORAGE_ACCOUNT" --query "id" > /storageAccountId
						"""#,
				]
				env: {
					AZURE_DEFAULTS_LOCATION: accountLocation
					AZURE_DEFAULTS_GROUP:    rgName
					AZURE_STORAGE_ACCOUNT:   accountName
				}
			},

			op.#Export & {
				source: "/storageAccountId"
				format: "string"
			},
		]
	} @dagger(output)
}
