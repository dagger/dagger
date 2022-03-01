package main

import (
	"strings"

	"dagger.io/dagger"
	"universe.dagger.io/bash"
)

dagger.#Plan & {

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
			source: {
				path: "../"
				exclude: ["./ci"]
			}
		}
	}

	outputs: directories: "go binaries": {
		contents: actions.build.export.directories["/build"]
		dest:     "./build"
	}

	actions: {
		_mountGoCache: {
			mounts: "go mod cache": {
				dest:     "/root/.gocache"
				contents: dagger.#CacheDir & {
					id: "go mod cache"
				}
			}
			env: GOMODCACHE: mounts["go mod cache"].dest
		}

		_mountSourceCode: {
			mounts: "dagger source code": {
				contents: inputs.directories.source.contents
				dest:     "/usr/src/dagger"
			}
			workdir: mounts["dagger source code"].dest
		}

		_baseImages: #Images

		build: bash.#Run & {
			_mountSourceCode
			_mountGoCache

			input: _baseImages.goBuilder

			env: {
				GOOS:   strings.ToLower(inputs.params.os)
				GOARCH: strings.ToLower(inputs.params.arch)
				// Makes sure the linter completes before starting the build
				"__depends": "\(goLint.exit)"
			}

			script: contents: #"""
				mkdir -p /build
				git_revision=$(git rev-parse --short HEAD)
				CGO_ENABLED=0 \
				 go build -v -o /build/dagger \
				 -ldflags '-s -w -X go.dagger.io/dagger/version.Revision='${git_revision} \
				 ./cmd/dagger/
				"""#

			export: directories: "/build": _
		}

		goLint: bash.#Run & {
			_mountSourceCode
			_mountGoCache

			input: _baseImages.goLinter

			script: contents: "golangci-lint run -v --timeout 5m"
		}

		cueLint: bash.#Run & {
			_mountSourceCode

			input: _baseImages.cue

			script: contents: #"""
				# Format the cue code
				find . -name '*.cue' -not -path '*/cue.mod/*' -print | time xargs -n 1 -P 8 cue fmt -s
				# Check that all formatted files where committed
				test -z $(git status -s . | grep -e '^ M'  | grep .cue | cut -d ' ' -f3)
				"""#
		}
	}
}
