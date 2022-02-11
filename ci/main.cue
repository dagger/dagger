package main

import (
	"strings"

	// "dagger.io/dagger"
	"dagger.io/dagger/engine"

	"universe.dagger.io/bash"
)

engine.#Plan & {
	inputs: {
		params: {
			// FIXME: until we support a better way
			os: string | *"darwin"
			arch: string | *"amd64"

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

			env: {
				GOMODCACHE: mounts["go mod cache"].dest
				GOOS: strings.ToLower(inputs.params.os)
				GOARCH: strings.ToLower(inputs.params.arch)
			}

			script: contents: #"""
				mkdir -p /build
				git_revision=$(git rev-parse --short HEAD)
				CGO_ENABLED=0 \
					go build -v -o /build/dagger \
					-ldflags '-s -w -X go.dagger.io/dagger/version.Revision='${git_revision} \
					./cmd/dagger/
				"""#

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
			export: directories: "/build": _
		}
	}
}
