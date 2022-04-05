package main

import (
	"dagger.io/dagger"

	"universe.dagger.io/bash"
	"universe.dagger.io/alpine"
	"universe.dagger.io/go"

	"github.com/dagger/dagger/ci/golangci"
	"github.com/dagger/dagger/ci/shellcheck"
	"github.com/dagger/dagger/ci/markdownlint"
	"github.com/dagger/dagger/ci/cue"
)

dagger.#Plan & {
	// FIXME: Ideally we would want to automatically set the platform's arch identical to the host
	// to avoid the performance hit caused by qemu (linter goes from <3s to >3m when arch is x86)
	// Uncomment if running locally on Mac M1 to bypass qemu
	// platform: "linux/aarch64"
	// platform: "linux/amd64"

	client: filesystem: ".": read: exclude: [
		"bin",
		"**/node_modules",
		"cmd/dagger/dagger",
		"cmd/dagger/dagger-debug",
	]
	client: filesystem: "./bin": write: contents: actions.build.output

	actions: {
		_source: client.filesystem["."].read.contents

		// FIXME: this can be removed once `go` supports built-in VCS info
		version: {
			_image: alpine.#Build & {
				packages: bash: _
				packages: curl: _
				packages: git:  _
			}

			_revision: bash.#Run & {
				input:   _image.output
				workdir: "/src"
				mounts: source: {
					dest:     "/src"
					contents: _source
				}

				script: contents: #"""
					printf "$(git rev-parse --short HEAD)" > /revision
					"""#
				export: files: "/revision": string
			}

			output: _revision.export.files["/revision"]
		}

		build: go.#Build & {
			source:  _source
			package: "./cmd/dagger/"
			os:      client.platform.os
			arch:    client.platform.arch

			ldflags: "-s -w -X go.dagger.io/dagger/version.Revision=\(version.output)"

			env: {
				CGO_ENABLED: "0"
				// Makes sure the linter and unit tests complete before starting the build
				// "__depends_lint":  "\(goLint.exit)"
				// "__depends_tests": "\(goTest.exit)"
			}
		}

		// Go unit tests
		test: go.#Test & {
			// container: image: _goImage.output
			source:  _source
			package: "./..."

			// FIXME: doesn't work with CGO_ENABLED=0
			// command: flags: "-race": true

			env: {
				// FIXME: removing this complains about lack of gcc
				CGO_ENABLED: "0"
			}
		}

		lint: {
			go: golangci.#Lint & {
				source:  _source
				version: "1.45"
			}

			shell: shellcheck.#Lint & {
				source: _source
			}

			markdown: markdownlint.#Lint & {
				source: _source
				files: ["./docs", "README.md"]
			}

			"cue": cue.#Lint & {
				source: _source
			}
		}
	}
}
