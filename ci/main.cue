package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/bash"
)

dagger.#Plan & {

	// FIXME: Ideally we would want to automatically set the platform's arch identical to the host
	// to avoid the performance hit caused by qemu (linter goes from <3s to >3m when arch is x86)
	platform: "linux/aarch64"

	client: filesystem: {
		"../": read: exclude: [
			"ci",
			"node_modules",
			"cmd/dagger/dagger",
			"cmd/dagger/dagger-debug",
		]
		"./build": write: contents: actions.build.export.directories["/build"]
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
				contents: client.filesystem."../".read.contents
				dest:     "/usr/src/dagger"
			}
			workdir: mounts["dagger source code"].dest
		}

		_baseImages: #Images

		// Go build the dagger binary
		// depends on goLint and goTest to complete successfully
		build: bash.#Run & {
			_mountSourceCode
			_mountGoCache

			input: _baseImages.goBuilder

			env: {
				GOOS:        client.platform.os
				GOARCH:      client.platform.arch
				CGO_ENABLED: "0"
				// Makes sure the linter and unit tests complete before starting the build
				"__depends_lint":  "\(goLint.exit)"
				"__depends_tests": "\(goTest.exit)"
			}

			script: contents: #"""
				mkdir -p /build
				git_revision=$(git rev-parse --short HEAD)
				go build -v -o /build/dagger \
				 -ldflags '-s -w -X go.dagger.io/dagger/version.Revision='${git_revision} \
				 ./cmd/dagger/
				"""#

			export: directories: "/build": _
		}

		// Go unit tests
		goTest: bash.#Run & {
			_mountSourceCode
			_mountGoCache

			input: _baseImages.goBuilder
			script: contents: "go test -race -v ./..."
		}

		// Go lint using golangci-lint
		goLint: bash.#Run & {
			_mountSourceCode
			_mountGoCache

			input: _baseImages.goLinter
			script: contents: "golangci-lint run -v --timeout 5m"
		}

		// CUE lint
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
