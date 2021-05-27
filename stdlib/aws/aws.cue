package aws

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/alpine"
)

// Base AWS Config
#Config: {
	// AWS region
	region: string @dagger(input)
	// AWS access key
	accessKey: dagger.#Secret @dagger(input)
	// AWS secret key
	secretKey: dagger.#Secret @dagger(input)
}

// Re-usable aws-cli component
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
				"package": "aws-cli": "=~1.18"
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
				aws configure set aws_access_key_id "$(cat /run/secrets/access_key)"
				aws configure set aws_secret_access_key "$(cat /run/secrets/secret_key)"

				aws configure set default.region "$AWS_DEFAULT_REGION"
				aws configure set default.cli_pager ""
				aws configure set default.output "json"
				"""#
			]
			mount: "/run/secrets/access_key": secret: config.accessKey
			mount: "/run/secrets/secret_key": secret: config.secretKey
			env: AWS_DEFAULT_REGION:    config.region
		},
	]
}