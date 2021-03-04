package cloudformation

import (
	"encoding/json"

	"dagger.io/dagger"
	"dagger.io/alpine"
	"dagger.io/aws"
)

// AWS CloudFormation Stack
#Stack: {

	// AWS Config
	config: aws.#Config

	// Source is the Cloudformation template (JSON/YAML string)
	source: string

	// Stackname is the cloudformation stack
	stackName: string

	// Stack parameters
	parameters: [string]: _

	// Behavior when failure to create/update the Stack
	onFailure: *"DO_NOTHING" | "ROLLBACK" | "DELETE"

	// Timeout for waiting for the stack to be created/updated (in minutes)
	timeout: *10 | uint

	// Never update the stack if already exists
	neverUpdate: *false | bool

	#files: {
		"/entrypoint.sh":     #Code
		"/src/template.json": source
		if len(parameters) > 0 {
			"/src/parameters.json": json.Marshal(
						[ for key, val in parameters {
									ParameterKey:   "\(key)"
									ParameterValue: "\(val)"
								}])
			"/src/parameters_overrides.json": json.Marshal([ for key, val in parameters {"\(key)=\(val)"}])
		}
	}

	outputs: {
		[string]: string

		#dagger: compute: [
		dagger.#Load & {
			from: alpine.#Image & {
				package: bash:      "=5.1.0-r0"
				package: jq:        "=1.6-r1"
				package: "aws-cli": "=1.18.177-r0"
			}
		},
		dagger.#Mkdir & {
			path: "/src"
		},
		for dest, content in #files {
			dagger.#WriteFile & {
				"dest":    dest
				"content": content
			}
		},
		dagger.#Exec & {
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"/entrypoint.sh",
			]
			env: {
				AWS_CONFIG_FILE:       "/cache/aws/config"
				AWS_ACCESS_KEY_ID:     config.accessKey
				AWS_SECRET_ACCESS_KEY: config.secretKey
				AWS_DEFAULT_REGION:    config.region
				AWS_REGION:            config.region
				AWS_DEFAULT_OUTPUT:    "json"
				AWS_PAGER:             ""
				if neverUpdate {
					NEVER_UPDATE: "true"
				}
				STACK_NAME: stackName
				TIMEOUT:    "\(timeout)"
				ON_FAILURE: onFailure
			}
			dir: "/src"
			mount: "/cache/aws": "cache"
		},
		dagger.#Export & {
			source: "/outputs.json"
			format: "json"
		},
	]
	}
}
