package cloudformation

import (
	"encoding/json"

	"dagger.io/dagger/op"
	"dagger.io/aws"
)

// AWS CloudFormation Stack
#Stack: {

	// AWS Config
	config: aws.#Config

	// Source is the Cloudformation template (JSON/YAML string)
	source: string @dagger(input)

	// Stackname is the cloudformation stack
	stackName: string @dagger(input)

	// Stack parameters
	parameters: [string]: _ @dagger(input)

	// Behavior when failure to create/update the Stack
	onFailure: *"DO_NOTHING" | "ROLLBACK" | "DELETE" @dagger(input)

	// Timeout for waiting for the stack to be created/updated (in minutes)
	timeout: *10 | uint @dagger(input)

	// Never update the stack if already exists
	neverUpdate: *false | bool @dagger(input)

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

	outputs: [string]: string @dagger(output)

	outputs: #up: [
		op.#Load & {
			from: aws.#CLI
		},
		op.#Mkdir & {
			path: "/src"
		},
		for dest, content in #files {
			op.#WriteFile & {
				"dest":    dest
				"content": content
			}
		},
		op.#Exec & {
			always: true
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
		op.#Export & {
			source: "/outputs.json"
			format: "json"
		},
	]
}
