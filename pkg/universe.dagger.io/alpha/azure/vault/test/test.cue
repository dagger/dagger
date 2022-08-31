package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/alpha/azure/aks/vault"
)

dagger.#Plan & {
	client: env: {
		AZURE_TENANT_ID:                     string
		AAD_SERVICE_PRINCIPAL_CLIENT_ID:     string
		AAD_SERVICE_PRINCIPAL_CLIENT_SECRET: dagger.#Secret
	}
	actions: secret: vault.#Get & {
		debug: true
		servicePrincipal: {
			tenantId:     client.env.AZURE_TENANT_ID
			clientId:     client.env.AAD_SERVICE_PRINCIPAL_CLIENT_ID
			clientSecret: client.env.AAD_SERVICE_PRINCIPAL_CLIENT_SECRET
		}
		vaultUri:      "https://myvault.vault.azure.net/"
		secretName:    "my-secret"
		secretVersion: "latest"
	}
}
