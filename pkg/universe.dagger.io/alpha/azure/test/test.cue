package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/docker"
	"universe.dagger.io/docker/cli"
	"universe.dagger.io/alpha/azure/auth"
	aksauth "universe.dagger.io/alpha/azure/aks/auth"
)

dagger.#Plan & {
	client: env: {
		AZURE_TENANT_ID:                     string
		AAD_SERVICE_PRINCIPAL_CLIENT_ID:     string
		AAD_SERVICE_PRINCIPAL_CLIENT_SECRET: dagger.#Secret
		AKS_SUSCRIPTION_ID:                  string
		AKS_CLUSTER:                         string
		AKS_RESOURCEGROUP:                   string
	}

	let sp = {
		tenantId: client.env.AZURE_TENANT_ID
		id:       client.env.AAD_SERVICE_PRINCIPAL_CLIENT_ID
		secret:   client.env.AAD_SERVICE_PRINCIPAL_CLIENT_SECRET
	}

	let cl = {
		subscriptionId: client.env.AKS_SUSCRIPTION_ID
		resourceGroup:  client.env.AKS_RESOURCEGROUP
		name:           client.env.AKS_CLUSTER
	}

	actions: {
		// check fi the images can be build successfully
		azureAuth: auth.#Image
		aksAuth:   aksauth.#Image

		// check if the token can be fetched (token is not used as of now)
		token: auth.#GetToken & {
			scope:            "https://vault.azure.net/.default"
			servicePrincipal: sp
		}

		// get a kubeconfig with admin crdentials
		kubeconfig: aksauth.#GetCredentials & {
			admin:            true
			servicePrincipal: sp
			cluster:          cl
		}

		// check if the config can be used to 
		// list all resources in the default namespace
		test: docker.#Run & {
			_img: docker.#Pull & {
				source: "bitnami/kubectl"
			}
			input: _img.output
			user:  "root"
			mounts: "/.kube/config": {
				dest:     "/.kube/config"
				type:     "secret"
				contents: kubeconfig.output
			}
			command: {
				name: "get"
				args: ["all", "-n", "default"]
			}
		}
	}
}
