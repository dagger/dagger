package yarn

import (
	"encoding/yaml"

	"dagger.io/dagger"
	"dagger.io/dagger/engine"

	"universe.dagger.io/netlify"
	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
)

dagger.#Plan & {
	inputs: secrets: sops: command: {
		name: "sops"
		args: ["-d", "../../test_secrets.yaml"]
	}

	actions: {
		secrets: engine.#TransformSecret & {
			input: inputs.secrets.sops.contents
			#function: {
				input:  _
				output: yaml.Unmarshal(input)
			}
		}

		marker: "hello world"

		data: engine.#WriteFile & {
			input:    engine.#Scratch
			path:     "index.html"
			contents: marker
		}

		// Deploy to netlify
		deploy: netlify.#Deploy & {
			team:  "blocklayer"
			token: secrets.output.netlify.token.contents

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
