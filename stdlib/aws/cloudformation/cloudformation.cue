// AWS CloudFormation
package cloudformation

import (
	"encoding/json"

	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/aws"
)

// AWS CloudFormation Stack
#Stack: {

	// AWS Config
	config: aws.#Config

	// Source is the Cloudformation template (JSON/YAML string)
	source: string & dagger.#Input

	// Stackname is the cloudformation stack
	stackName: string & dagger.#Input

	// Stack parameters
	parameters: {
		...
	} & dagger.#Input

	// Behavior when failure to create/update the Stack
	onFailure: *"DO_NOTHING" | "ROLLBACK" | "DELETE" & dagger.#Input

	// Maximum waiting time until stack creation/update (in minutes)
	timeout: *10 | uint & dagger.#Input

	// Never update the stack if already exists
	neverUpdate: *false | true & dagger.#Input

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

	outputs: [string]: string & dagger.#Output

	outputs: #up: [
		op.#Load & {
			from: aws.#CLI & {
				"config": config
			}
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
				if neverUpdate {
					NEVER_UPDATE: "true"
				}
				STACK_NAME: stackName
				TIMEOUT:    "\(timeout)"
				ON_FAILURE: onFailure
			}
			dir: "/src"
		},
		op.#Export & {
			source: "/outputs.json"
			format: "json"
		},
	]
}
