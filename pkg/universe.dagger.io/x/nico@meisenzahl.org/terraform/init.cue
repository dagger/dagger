// Deploy with Terraform
// https://www.terraform.io

// TODO:
// - Add support for injecting generic backend configuration secrets

package terraform

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/docker"
)

// Terraform init
#Init: {
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

			// Run init
			docker.#Run & {
				command: {
					name: "init"
					args: ["-input=false", "-upgrade"]
				}
				workdir: "/src"
			},
		]
	}

	// Output source and .terraform folder
	_output: core.#Subdir & {
		input: _run.output.rootfs
		path:  "/src/"
	}

	// Output source and .terraform folder
	output: _output.output
}
