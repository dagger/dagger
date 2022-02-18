package main

import (
	"strings"

	"dagger.io/dagger/engine"
	"universe.dagger.io/bash"
)

engine.#Plan & {

	// FIXME: Ideally we would want to automatically set the platform's arch identical to the host
	// to avoid the performance hit caused by qemu (linter goes from <3s to >3m when arch is x86)
	platform: "linux/aarch64"

	inputs: {
		params: {
			// FIXME: until we support a better way
			os:   string | *"darwin"
			arch: string | *"amd64"

			// FIXME: implement condition actions using params until we have a
			// better way to select specific actions
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
		_goModCache: "go mod cache": {
			dest:     "/gomodcache"
			contents: engine.#CacheDir & {
				id: "go mod cache"
			}
		}

		_baseImages: #Images

		_source: "dagger source code": {
			contents: inputs.directories.source.contents
			dest:     "/usr/src/dagger"
		}

		// FIXME: build only if the linter passed
		build: bash.#Run & {
			input: _baseImages.goBuilder

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

			workdir: mounts["dagger source code"].dest
			mounts: {
				_source
				_goModCache
			}

			export: directories: "/build": _
		}

		goLint: bash.#Run & {
			input: _baseImages.goLinter

			script: contents: "golangci-lint run -v --timeout 5m"
			workdir: mounts["dagger source code"].dest
			mounts: {
				_source
				_goModCache
			}
		}
	}
}
