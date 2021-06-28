// Terraform operations
package terraform

import (
	"encoding/json"

	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
)

// Terraform configuration
#Configuration: {

	// Terraform version
	version: string | *"latest" @dagger(input)

	// Source configuration
	source: dagger.#Artifact @dagger(input)

	tfvars?: {
		...
	}

	// Environment variables
	env: {
		[string]: string @dagger(input)
	}

	state: #up: [
		op.#FetchContainer & {
			ref: "hashicorp/terraform:\(version)"
		},

		op.#Copy & {
			from: source
			dest: "/src"
		},

		if tfvars != _|_ {
			op.#WriteFile & {
				dest:    "/src/terraform.tfvars.json"
				content: json.Marshal(tfvars)
			}
		},

		op.#Exec & {
			args: ["terraform", "init"]
			dir:   "/src"
			"env": env
		},

		op.#Exec & {
			args: ["terraform", "apply", "-auto-approve"]
			always: true
			dir:    "/src"
			"env":  env
		},
	]

	output: {
		#up: [
			op.#Load & {from: state},
			op.#Exec & {
				args: ["sh", "-c", "terraform output -json > /output.json"]
				dir:   "/src"
				"env": env
			},
			op.#Export & {
				source: "/output.json"
				format: "json"
			},
		]
		...
	} @dagger(output)
}
