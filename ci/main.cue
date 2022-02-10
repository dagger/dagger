package main

import (
	// "dagger.io/dagger"
	"dagger.io/dagger/engine"

	"universe.dagger.io/bash"
)

engine.#Plan & {
	inputs: {
		params: {
			// FIXME: implement condition actions using params
		}

		directories: {
			// dagger repository
			source: path: "../"
		}
	}

	outputs: directories: "go binaries": {
		contents: actions.build.export.directories["/build"].contents
		dest:     "./build"
	}

	actions: {
		goModCache: engine.#CacheDir & {
			id: "go mod cache"
		}

		build: bash.#Run & {
			input: images.goBuilder.output

			script: contents: #"""
				export GOMODCACHE=/gomodcache
				mkdir -p /build
				git_revision=$(git rev-parse --short HEAD)
				GO_ENABLED=0 \
					go build -v -o /build/dagger \
					-ldflags '-s -w -X go.dagger.io/dagger/version.Revision='${git_revision} \
					./cmd/dagger/
				"""#

			export: directories: "/build": _
			workdir: "/usr/src/dagger"
			env: GOMODCACHE: "/gomodcache"

			mounts: {
				"dagger source code": {
					contents: inputs.directories.source.contents
					dest:     "/usr/src/dagger"
				}

				"go mod cache": {
					dest:     "/gomodcache"
					contents: goModCache
				}
			}

			workdir: mounts["dagger source code"].dest
		}
	}
}
