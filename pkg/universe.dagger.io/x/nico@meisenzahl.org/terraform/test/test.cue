package terraform

import (
	"dagger.io/dagger"

	"universe.dagger.io/x/nico@meisenzahl.org/terraform"
)

let tfversion = "1.1.7"

dagger.#Plan & {
	client: filesystem: "./data": read: contents: dagger.#FS

	actions: test: {
		// Terraform init
		init: terraform.#Init & {
			source:  client.filesystem."./data".read.contents
			version: tfversion // defaults to latest
		}

		// Terraform plan
		plan: terraform.#Plan & {
			source:  init.output
			version: tfversion
		}

		// Implement any further actions here. tfsec, trivy, you name it.

		// Terraform apply
		apply: terraform.#Apply & {
			source:  plan.output
			version: tfversion
		}
	}
}
