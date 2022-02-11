package main

import (
	"strings"

	"dagger.io/dagger/engine"
	"universe.dagger.io/bash"
)

engine.#Plan & {
	inputs: {
		params: {
			// FIXME: until we support a better way
			os:   string | *"darwin"
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

		source: "dagger source code": {
			contents: inputs.directories.source.contents
			dest:     "/usr/src/dagger"
		}

		// FIXME: build only if the linter passed
		build: bash.#Run & {
			input: images.goBuilder.output

			env: {
				GOMODCACHE: mounts["go mod cache"].dest
				GOOS:       strings.ToLower(inputs.params.os)
				GOARCH:     strings.ToLower(inputs.params.arch)
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
				source

				"go mod cache": {
					dest:     "/gomodcache"
					contents: goModCache
				}
			}

			workdir: mounts["dagger source code"].dest
			export: directories: "/build": _
		}

		goLint: bash.#Run & {
			input: images.goLinter.output

			// FIXME: the source volume is too slow, taking >3m on docker for mac (vs < 2sec on the host machine)
			script: contents: "golangci-lint run -v --timeout 5m"
			workdir: mounts["dagger source code"].dest
			mounts: {
				source
			}
		}
	}
}
