package netlify

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"

	"universe.dagger.io/docker"
	"universe.dagger.io/netlify"

	"universe.dagger.io/netlify/test/testutils"
)

dagger.#Plan & {
	inputs: secrets: test: command: {
		name: "sops"
		args: ["-d", "../../test_secrets.yaml"]
	}

	actions: tests: {

		// Configuration common to all tests
		common: {
			testSecrets: dagger.#DecodeSecret & {
				input:  inputs.secrets.test.contents
				format: "yaml"
			}

			token: testSecrets.output.netlifyToken.contents

			marker: "hello world"

			data: engine.#WriteFile & {
				input:    engine.#Scratch
				path:     "index.html"
				contents: marker
			}
		}

		// Test: deploy a simple site to Netlify
		simple: {
			// Deploy to netlify
			deploy: netlify.#Deploy & {
				team:     "blocklayer"
				token:    common.token
				site:     "dagger-test"
				contents: common.data.output
			}

			verify: testutils.#AssertURL & {
				url:      deploy.deployUrl
				contents: common.marker
			}
		}

		// Test: deploy to Netlify with a custom image
		swapImage: {
			// Deploy to netlify
			deploy: netlify.#Deploy & {
				team:     "blocklayer"
				token:    common.token
				site:     "dagger-test"
				contents: common.data.output
				container: input: customImage.output
			}

			customImage: docker.#Build & {
				steps: [
					docker.#Pull & {
						source: "alpine"
					},
					docker.#Run & {
						command: {
							name: "apk"
							args: [
								"add",
								"--no-cache",
								"yarn",
								"bash",
								"rsync",
								"curl",
								"jq",
							]
						}
					},
					docker.#Run & {
						command: {
							name: "yarn"
							args: ["global", "add", "netlify-cli"]
						}
					},
				]
			}

			verify: testutils.#AssertURL & {
				url:      deploy.deployUrl
				contents: common.marker
			}
		}
	}
}
