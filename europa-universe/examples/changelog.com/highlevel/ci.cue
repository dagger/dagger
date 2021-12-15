package ci

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"

	"universe.dagger.io/docker"
	"universe.dagger.io/examples/changelog.com/highlevel/elixir/mix"
)

dagger.#Plan & {
	// Receive things from client
	inputs: {
		directories: {
			// App source code
			app: _
		}
		secrets: {
			// Docker ID password
			docker: _
		}
		params: {
			// Which Elixir base image to download
			runtime_image: docker.#Ref | *"thechangelog/runtime:2021-05-29T10.17.12Z"
			// Which test DB image to download
			test_db_image: docker.#Ref | *"circleci/postgres:12.6"
		}
	}

	// Do things
	actions: {
		// Reuse in all mix commands
		_appName: "changelog"

		prod: assets: docker.#Build & {
			steps: [
				// 1. Start from dev assets :)
				dev.assets,
				// 2. Mix magical command
				mix.#Run & {
					script: "mix phx.digest"
					mix: {
						env:        "prod"
						app:        _appName
						depsCache:  "readonly"
						buildCache: "readonly"
					}
					workdir: _
					// FIXME: remove copy-pasta
					mounts: nodeModules: {
						contents: engine.#CacheDir & {
							// FIXME: do we need an ID here?
							id: "\(mix.app)_assets_node_modules"
							// FIXME: does this command need write access to node_modules cache?
							concurrency: "readonly"
						}
						dest: "\(workdir)/node_modules"
					}
				},
			]
		}

		dev: {
			build: mix.#Build & {
				env:    "dev"
				app:    "thechangelog"
				base:   inputs.params.runtime_image
				source: inputs.directories.app.contents
			}

			assets: docker.#Build & {
				steps: [
					// 1. Start from dev runtime build
					build,
					// 2. Build web assets
					mix.#Run & {
						mix: {
							env:        "dev"
							app:        _appName
							depsCache:  "readonly"
							buildCache: "readonly"
						}
						// FIXME: move this to a reusable def (yarn package? or private?)
						mounts: nodeModules: {
							contents: engine.#CacheDir & {
								// FIXME: do we need an ID here?
								id: "\(mix.app)_assets_node_modules"
								// FIXME: will there be multiple writers?
								concurrency: "locked"
							}
							dest: "\(workdir)/node_modules"
						}
						// FIXME: run 'yarn install' and 'yarn run compile' separately, with different caching?
						// FIXME: can we reuse universe.dagger.io/yarn ???? 0:-)
						script:  "yarn install --frozen-lockfile && yarn run compile"
						workdir: "/app/assets"
					},
				]
			}
		}
		test: {
			build: mix.#Build & {
				env:    "test"
				app:    _appName
				base:   inputs.params.runtime_image
				source: inputs.directories.app.contents
			}

			// Run tests
			run: docker.#Run & {
				image:  build.output
				script: "mix test"
				// Don't cache running tests
				// Just because we've tested a version before, doesn't mean we don't
				// want to test it again.
				// FIXME: make this configurable
				always: true
			}

			db: {
				// Pull test DB image
				pull: docker.#Pull & {
					source: inputs.params.test_db_image
				}

				// Run test DB
				// FIXME: kill once no longer needed (when tests are done running)
				run: docker.#Run & {
					image: pull.output
				}
			}
		}
	}
}
