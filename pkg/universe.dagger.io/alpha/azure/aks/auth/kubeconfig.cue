package auth

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

#GetCredentials: {
	_img: #Image

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
	cluster: {
		subscriptionId: string
		name:           string
		resourceGroup:  string
	}

	// Use the configured service principal to
	// fetch the kubeconfig
	servicePrincipal: {
		tenantId: string
		id:       string
		secret:   dagger.#Secret
	}

	// the output contains the kubeconfig as secretr
	output: _run.export.secrets."/kubeconfig"

	_run: docker.#Run & {
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
			AZURE_TENANT_ID:                     servicePrincipal.tenantId
			AAD_SERVICE_PRINCIPAL_CLIENT_ID:     servicePrincipal.id
			AAD_SERVICE_PRINCIPAL_CLIENT_SECRET: servicePrincipal.secret
			AKS_SUSCRIPTION_ID:                  cluster.subscriptionId
			AKS_RESOURCE_GROUP:                  cluster.resourceGroup
			AKS_NAME:                            cluster.name
		}
		export: secrets: "/kubeconfig": _
	}
}
