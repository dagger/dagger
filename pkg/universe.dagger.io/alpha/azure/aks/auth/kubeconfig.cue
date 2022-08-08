package auth

import (
	"universe.dagger.io/docker"
	"universe.dagger.io/alpha/azure/auth"
)

#Cluster: {
	subscriptionId: string
	name:           string
	resourceGroup:  string
}

#GetCredentials: {
	_img: #Image

	// enable debug output
	debug: *false | true

	// Get admin cedentials, if true
	// otherwise user credentials
	admin: *false | true

	// if user crdentials are used,
	// the format of the kubeconfig can be set
	// exec requires kubelogin to be present
	// ref: WARNING: the azure auth plugin is deprecated in v1.22+,
	// unavailable in v1.25+; use https://github.com/Azure/kubelogin instead.
	format: *"exec" | "azure"

	// Fetch the config for the configured cluster
	cluster: #Cluster

	// Use the configured service principal to
	// fetch the kubeconfig
	servicePrincipal: auth.#ServicePrincipal

	// the output contains the kubeconfig as secretr
	output: _run.creds.export.secrets."/kubeconfig"

	_run: {
		token: auth.#AccessToken & {
			"servicePrincipal": servicePrincipal
			"debug":            debug
		}
		creds: docker.#Run & {
			input: _img.output
			command: {
				name: "akscreds"
				args: [
					if admin {"-a"},
					"-f",
					format,
					"-o",
					"/kubeconfig",
				]
			}
			env: {
				AAD_ACCESS_TOKEN:   token.output
				AKS_SUSCRIPTION_ID: cluster.subscriptionId
				AKS_RESOURCE_GROUP: cluster.resourceGroup
				AKS_NAME:           cluster.name
				AZURE_DEBUG:        [ if debug {"1"}, "0"][0]

			}
			export: secrets: "/kubeconfig": _
		}
	}
}
