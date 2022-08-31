package vault

import (
	"universe.dagger.io/docker"
	"universe.dagger.io/alpha/azure/auth"
)

#Get: {
	// enable debug output
	debug: *false | true
	// the service principal to use
	servicePrincipal: auth.#ServicePrincipal
	// the url of the vault as written in the azure portal
	vaultUri: string
	// the name of the secret
	secretName: string
	// optional version of the secret
	secretVersion: *"latest" | string
	// the secret is exported as output field
	output: _run.export.secrets."/value"

	_token: auth.#AccessToken & {
		"debug":            debug
		resource:           "https://vault.azure.net"
		scope:              "https://vault.azure.net/.default"
		"servicePrincipal": servicePrincipal
	}
	_img: #Image
	_run: docker.#Run & {
		input: _img.output
		entrypoint: ["/bin/sh", "-c"]
		command: name: "get-secret \(secretName) \(secretVersion) > /value"
		env: {
			AAD_ACCESS_TOKEN: _token.output
			AZURE_VAULT_URI:  vaultUri
			AZURE_DEBUG:      [ if debug {"1"}, "0"][0]

		}
		export: secrets: "/value": _
	}
}
