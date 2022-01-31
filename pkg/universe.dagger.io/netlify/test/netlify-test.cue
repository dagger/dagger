package yarn

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"

	"universe.dagger.io/netlify"
	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
)

dagger.#Plan & {
	inputs: secrets: testSecrets: command: {
		name: "sops"
		args: ["exec-env", "../../test_secrets.yaml", "echo $netlifyToken"]
	}

	actions: {
		marker: "hello world"

		data: engine.#WriteFile & {
			input:    engine.#Scratch
			path:     "index.html"
			contents: marker
		}

		// Deploy to netlify
		deploy: netlify.#Deploy & {
			team:  "blocklayer"
			token: inputs.secrets.testSecrets.contents

			site:     "dagger-test"
			contents: data.output
		}

		_alpine: alpine.#Build & {
			packages: {
				bash: {}
				curl: {}
			}
		}

		// Check if the website was deployed
		verify: bash.#Run & {
			input:  _alpine.output
			script: #"""
		  test "$(curl \#(deploy.deployUrl))" = "\#(marker)"
		  """#
		}
	}
}
