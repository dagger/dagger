package jaeger

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	// NOTE: this assumes the user has setup gh credentials previously via "gh auth login"
	client: filesystem: "~/.config/gh/hosts.yml": read: contents?: dagger.#Secret

	actions: {
		// Load client GH credentials, download jaeger_data artifacts from a GHA run, start
		// jaeger with that data. This will just block indefinitely, can ctrl-C to stop it.
		// The jaeger daemon will be serving on the localhost of the machine running Buildkit
		// and can be visited at http://localhost:16686
		jaegerView: {
			runID:        string
			repository:   string | *"dagger/dagger"
			artifactName: string | *"jaeger_data"

			_ghImage: alpine.#Build & {
				packages: "github-cli": _
			}
			_dl: docker.#Run & {
				input: _ghImage.output
				mounts: gh: {
					dest:     "/root/.config/gh/hosts.yml"
					contents: client.filesystem["~/.config/gh/hosts.yml"].read.contents
				}
				command: {
					name: "gh"
					args: [
						"run",
						"download",
						runID,
						"-n", artifactName,
						"-R", repository,
						"-D", "/output",
					]
				}
				// Re-running a GHA run doesn't change any
				// IDs even though the artifact can change,
				// so always run this step
				always: true
			}
			_jaegerData: core.#Subdir & {
				input: _dl.output.rootfs
				path:  "/output"
			}

			_jaegerImage: docker.#Pull & {
				source: "jaegertracing/all-in-one:1.33.0"
			}
			run: docker.#Run & {
				input: _jaegerImage.output
				mounts: data: {
					dest:     "/badger"
					contents: _jaegerData.output
				}
				env: {
					SPAN_STORAGE_TYPE:      "badger"
					BADGER_EPHEMERAL:       "false"
					BADGER_DIRECTORY_VALUE: "/badger/data"
					BADGER_DIRECTORY_KEY:   "/badger/key"
				}
			}
		}
	}
}
