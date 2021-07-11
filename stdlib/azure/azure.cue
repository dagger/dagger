package azure

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
)

#Config: {
	// AZURE region
	region: string @dagger(input)
	// AZURE tenant id
	tenantId: string @dagger(input)
	// AZURE subscription id
	subscriptionId: dagger.#Secret @dagger(input)
	// AZURE app id for the service principal used
	appId: dagger.#Secret @dagger(input)
	// AZURE password for the service principal used
	password: dagger.#Secret @dagger(input)
}

#CLI: {
	config: #Config
	package: [string]: string | bool

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				"package": package
				"package": bash:          "=~5.1"
				"package": jq:            "=~1.6"
				"package": python3:       "=~3.8"
				"package": "python3-dev": true
				"package": "py3-pip":     true
				"package": openssl:       true
				"package": gcc:           true
				"package": make:          true
				"package": "openssl-dev": true
				"package": "libffi-dev":  true
				"package": "musl-dev":    true
			}
		},

		op.#Exec & {
			args: [
				"sh", "-c",
				#"""
					 pip install azure-cli==2.26.0
					"""#,
			]
		},

		op.#Exec & {
			args: ["az", "login", "--service-principal", "-u", "$(cat /run/secrets/appId)", "-p", "$(cat /run/secrets/password)", "-t", config.tenantId]
			mount: "/run/secrets/appId": secret:    config.appId
			mount: "/run/secrets/password": secret: config.password
		},

		op.#Exec & {
			args: ["az", "account", "set", "-s", "$(cat /run/secrets/subscriptionId)"]
			mount: "/run/secrets/subscriptionId": secret: config.subscriptionId
		},
	]

}
