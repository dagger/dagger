package auth

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

#ServicePrincipal: {
	tenantId:     string
	clientId:     string
	clientSecret: dagger.#Secret
}

#AccessToken: {
	_img: #Image

	debug: *false | true

	// The resource to get the token for
	// default to https://management.azure.com/
	resource: string | *""
	// The scope to get the token for
	// for non generic token, i.e. vault has the scope
	// https://vault.azure.net/.default
	scope: string | *""

	// The service principal used to get the token
	servicePrincipal: #ServicePrincipal

	// The output contains the token in a secret
	output: _run.export.secrets."/token"

	_run: docker.#Run & {
		input: _img.output
		command: {
			name: "sh"
			args: ["-c", "azlogin > /token"]
		}
		env: {
			AZLOGIN_RESOURCE:                    resource
			AZLOGIN_SCOPE:                       scope
			AZURE_TENANT_ID:                     servicePrincipal.tenantId
			AAD_SERVICE_PRINCIPAL_CLIENT_ID:     servicePrincipal.clientId
			AAD_SERVICE_PRINCIPAL_CLIENT_SECRET: servicePrincipal.clientSecret
			AZURE_DEBUG:                         [ if debug {"1"}, "0"][0]
		}
		export: secrets: "/token": _
	}
}
