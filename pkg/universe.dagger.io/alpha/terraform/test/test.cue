package terraform

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/docker"
	"universe.dagger.io/alpha/terraform"
)

dagger.#Plan & {
	actions: test: {
		tfSource: core.#Source & {
			path: "./data"
		}

		tfImportSource: core.#Source & {
			path: "./import_data"
		}

		applyWorkflow: {
			init: terraform.#Init & {
				source: tfSource.output
			}

			validate: terraform.#Validate & {
				source: init.output
			}

			plan: terraform.#Plan & {
				source: validate.output
			}

			apply: terraform.#Apply & {
				source: plan.output
			}

			verifyOutput: #AssertFile & {
				input:    apply.output
				path:     "./out.txt"
				contents: "Hello, world!"
			}

			verifyDefaultVersion: #AssertFileRegex & {
				input: apply.output
				path:  "terraform.tfstate"
				regex: string | *"1\\.2\\.7"
			}
		}

		destroyWorkflow: {
			destroy: terraform.#Destroy & {
				source: applyWorkflow.apply.output
			}

			// TODO assert out.txt doesn't exist
		}

		importWorkflow: {
			init: terraform.#Init & {
				source: tfImportSource.output
			}
			importResource: terraform.#Import & {
				source:  init.output
				address: "random_uuid.test"
				id:      "aabbccdd-eeff-0011-2233-445566778899"
			}
		}

		workspaceWorkflow: {
			init: terraform.#Init & {
				source: tfImportSource.output
			}

			workspaceNew: terraform.#Workspace & {
				source: init.output
				subCmd: "new"
				name:   "TEST_WORKSPACE"
			}

			workspaceList: terraform.#Workspace & {
				source: workspaceNew.output
				subCmd: "list"
			}

			workspaceShow: terraform.#Workspace & {
				source: workspaceNew.output
				subCmd: "show"
				name:   "TEST_WORKSPACE"
			}

			workspaceShowNoSubCmd: terraform.#Workspace & {
				source: workspaceNew.output
				subCmd: "show"
			}

			workspaceSelect: terraform.#Workspace & {
				source: workspaceNew.output
				subCmd: "select"
				name:   "default"
			}

			workspaceDelete: terraform.#Workspace & {
				source: workspaceSelect.output
				subCmd: "delete"
				name:   "TEST_WORKSPACE"
			}
		}

		versionWorkflow: {
			// Set a terraform version for testing specific version of terraform
			terraformVersion?: string

			init: terraform.#Init & {
				source:  tfSource.output
				version: "1.2.6"
			}

			plan: terraform.#Plan & {
				source:  init.output
				version: "1.2.6"
			}

			apply: terraform.#Apply & {
				always:  true
				source:  plan.output
				version: "1.2.6"
			}

			verify: #AssertFileRegex & {
				input: apply.output
				path:  "terraform.tfstate"
				regex: string | *"1\\.2\\.6"
			}
		}

		imageWorkflow: {
			// Set a terraform version for testing specific version of terraform
			_image: docker.#Pull & {
				source: "hashicorp/terraform:1.2.7"
			}

			init: terraform.#Init & {
				container: #input: _image.output
				source: tfSource.output
			}

			workspaceNew: terraform.#Workspace & {
				container: #input: _image.output
				source: init.output
				subCmd: "new"
				name:   "TEST_WORKSPACE"
			}

			plan: terraform.#Plan & {
				container: #input: _image.output
				source: init.output
			}

			apply: terraform.#Apply & {
				container: #input: _image.output
				always: true
				source: plan.output
			}

			verify: #AssertFileRegex & {
				input: apply.output
				path:  "terraform.tfstate"
				regex: "1\\.2\\.7"
			}

			destroy: terraform.#Destroy & {
				container: #input: _image.output
				source: apply.output
				container: #input: _image.output
			}
		}
	}
}

// Make an assertion on the contents of a file
#AssertFile: {
	input:    dagger.#FS
	path:     string
	contents: string

	_read: core.#ReadFile & {
		"input": input
		"path":  path
	}

	actual: _read.contents

	// Assertion
	contents: actual
}

// Make an assertion on the contents of a file
#AssertFileRegex: {
	input: dagger.#FS
	path:  string
	regex: string

	_read: core.#ReadFile & {
		"input": input
		"path":  path
	}

	actual: _read.contents

	contents: =~regex
	// Assertion
	contents: actual
}
