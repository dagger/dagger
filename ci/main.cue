package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/bash"
)

dagger.#Plan & {

	client: {
		filesystem: "./build": write: contents: actions.build.export.directories["/build"]
		env: {
			// ⚠️  If this is set to darwin on a Mac ARM, expect to pay the qemu penalty and be ~50x slower.
			// FWIW, this runs in a BuildKit container within Docker.
			// Docker on macOS runs within a Linux VM, hence:
			// Darwin -> Linux -> Darwin => ~50x slower than native Darwin
			GOOS:   string | *"linux"
			GOARCH: string | client.platform.arch
		}
	}

	actions: {
		_mountGoCache: {
			mounts: "go mod cache": {
				dest:     "/root/.gocache"
				contents: core.#CacheDir & {
					id: "go mod cache"
				}
			}
			env: GOMODCACHE: mounts["go mod cache"].dest
		}

		_mountSourceCode: {
			mounts: "dagger source code": {
				contents: _source.output
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
				GOOS:        client.env.GOOS
				GOARCH:      client.env.GOARCH
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
