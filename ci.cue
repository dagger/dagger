package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/bash"
	"universe.dagger.io/alpine"
	"universe.dagger.io/go"

	"github.com/dagger/dagger/ci/golangci"
	"github.com/dagger/dagger/ci/shellcheck"
	"github.com/dagger/dagger/ci/markdownlint"
	"github.com/dagger/dagger/ci/cue"
	"github.com/dagger/dagger/ci/bats"
)

dagger.#Plan & {

	client: filesystem: ".": read: exclude: [
		"bin",
		"**/node_modules",
		"cmd/dagger/dagger",
		"cmd/dagger/dagger-debug",
	]
	client: filesystem: "./": read: {
		contents: dagger.#FS
		exclude: ["website"]
	}
	client: filesystem: "./bin": write: contents: actions.build."go".output

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

		build: {
			"go": go.#Build & {
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
			docker: core.#Dockerfile & {
				source: client.filesystem["./"].read.contents
				dockerfile: path: "Dockerfile"
			}
		}

		// Go unit tests
		test: go.#Test & {
			// container: image: _goImage.output
			source:  _source
			package: "./..."

			command: flags: "-race": true
		}

		integration: bats.#Bats & {
			source: _source
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
