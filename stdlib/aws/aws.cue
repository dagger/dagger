package aws

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/alpine"
)

// Base AWS Config
#Config: {
	// AWS region
	region: string
	// AWS access key
	accessKey: dagger.#Secret
	// AWS secret key
	secretKey: dagger.#Secret
}

// Re-usable aws-cli component
#CLI: {
	package: [string]: string

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				"package": package
				"package": bash:      "=~5.1"
				"package": jq:        "=~1.6"
				"package": curl:      "=~7.76"
				"package": "aws-cli": "=~1.18"
			}
		},
	]
}

// Helper for writing scripts based on AWS CLI
#Script: {
	// AWS code
	config: #Config

	// Script code (bash)
	code: string

	// Extra pkgs to install
	package: [string]: string

	// Files to mount
	files: [string]: string

	// Env variables
	env: [string]: string

	// Export file
	export: string

	// Always execute the script?
	always?: bool

	out: {
		string

		#up: [
			op.#Load & {
				from: #CLI & {
					"package": package
				}
			},
			op.#Mkdir & {
				path: "/inputs"
			},
			for k, v in files {
				op.#WriteFile & {
					dest:    k
					content: v
				}
			},
			op.#WriteFile & {
				dest:    "/entrypoint.sh"
				content: code
			},
			op.#Exec & {
				if always != _|_ {
					"always": always
				}
				args: [
					"/bin/bash",
					"--noprofile",
					"--norc",
					"-eo",
					"pipefail",
					"/entrypoint.sh",
				]
				"env": env
				"env": {
					AWS_CONFIG_FILE:       "/cache/aws/config"
					AWS_ACCESS_KEY_ID:     config.accessKey
					AWS_SECRET_ACCESS_KEY: config.secretKey
					AWS_DEFAULT_REGION:    config.region
					AWS_REGION:            config.region
					AWS_DEFAULT_OUTPUT:    "json"
					AWS_PAGER:             ""
				}
				mount: "/cache/aws": "cache"
			},
			op.#Export & {
				source: export
				format: "string"
			},
		]
	}
}
