package changelog

import (
	"dagger.io/dagger"

	"universe.dagger.io/docker"
	"universe.dagger.io/git"
	"universe.dagger.io/examples/changelog.com/elixir/mix"
)

dagger.#Plan & {
	// Receive things from client
	inputs: {
		directories: {
			// App source code
			app?: _
		}
		secrets: {
			// Docker ID password
			docker: _
		}
		params: {
			app: {
				// App name
				name: string | *"changelog"

				// Address of app base image
				image: docker.#Ref | *"thechangelog/runtime:2021-05-29T10.17.12Z"
			}

			test: {
				// Address of test db image
				db: image: docker.#Ref | *"circleci/postgres:12.6"
			}

		}
	}

	// Do things
	actions: {
		app: {
			name: inputs.params.app.name

			// changelog.com source code
			source: dagger.#FS
			if inputs.directories.app != _|_ {
				source: inputs.directories.app.contents
			}
			if inputs.directories.app == _|_ {
				fetch: git.#Pull & {
					remote: "https://github.com/thechangelog/changelog.com"
					ref:    "master"
				}
				source: fetch.output
			}

			// Assemble base image
			base: docker.#Pull & {
				source: inputs.params.app.image
			}
			image: base.output

			// Download Elixir dependencies
			deps: mix.#Get & {
				app: {
					"name":   name
					"source": source
				}
				container: "image": image
			}

			// Compile dev environment
			dev: mix.#Compile & {
				env: "dev"
				app: {
					"name":   name
					"source": source
				}
				container: "image": image
			}
		}
	}
}
