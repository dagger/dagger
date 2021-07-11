// AWS base package
package azure

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
)

// Azure Config shared by all AZURE packages
#Config: {
	// AZURE region
	region: string @dagger(input)
	// AZURE username 
	username: dagger.#Secret @dagger(input)
	// AZURE tenant id
	tenantid: dagger.#Secret @dagger(input)
	// AZURE password
	password: dagger.#Secret @dagger(input)
}

// Re-usable azure-cli component
#CLI: {
	config: #Config
	package: [string]: string | bool

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				"package": package
				"package": bash:      "=~5.1"
				"package": jq:        "=~1.6"
				"package": curl:      true
                "package": py-pip:    true 
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
                    pip --no-cache-dir install -U pip
                    pip --no-cache-dir install azure-cli
                    az login --service-principal -u "$(cat /run/secrets/username)" -p "$(cat /run/secrets/password)" --tenant "$(cat /run/secrets/tenantid)"
					"""#,
			]
			mount: "/run/secrets/username": secret: config.username
			mount: "/run/secrets/password": secret: config.password
            mount: "/run/secrets/tenantid": secret: config.tenantid
            az account show >&2
			// env: AWS_DEFAULT_REGION: config.region
		},
	]
}
