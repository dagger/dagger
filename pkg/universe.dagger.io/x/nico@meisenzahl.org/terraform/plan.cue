// Deploy with Terraform
// https://www.terraform.io

// TODO:
// - Add support for TF_VARS_* environment variables and *.tvars files

package terraform

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/docker"
)

// Terraform plan
#Plan: {
	// Terraform version
	version: string | *"latest"

	// Source code
	source: dagger.#FS

	_run: docker.#Build & {
		steps: [
			docker.#Pull & {
				source: "hashicorp/terraform:\(version)"
			},

			// Copy source
			docker.#Copy & {
				dest:     "/src"
				contents: source
			},

			// Run plan
			docker.#Run & {
				command: {
					name: "plan"
					args: ["-input=false", "-out=./tfplan.out"]
				}
				workdir: "/src"
			},
		]
	}

	// Output plan file
	_output: core.#Subdir & {
		input: _run.output.rootfs
		path:  "/src/"
	}

	// Output plan file
	output: _output.output
}
