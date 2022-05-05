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
)

dagger.#Plan & {

	client: filesystem: ".": read: exclude: [
		"bin",
		"**/node_modules",
		"cmd/dagger/dagger",
		"cmd/dagger/dagger-debug",
		"website",
	]
	client: filesystem: "./": read: {
		include: [
			"./website",
		]
		exclude: ["**/node_modules"]
	}

	client: filesystem: "./bin": write: contents: actions.build."go".output

	actions: {
		_source:  client.filesystem["."].read.contents
		_website: core.#Merge & {
			inputs: [_source, client.filesystem."./".read.contents]
		}

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
					contents: _website.output
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
				source:  _website.output
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
				source: _source
				dockerfile: path: "Dockerfile"
			}

		}

		// Go unit tests
		test: go.#Test & {
			// container: image: _goImage.output
			source:  _website.output
			package: "./..."

			command: flags: "-race": true
		}

		lint: {
			go: golangci.#Lint & {
				source:  _website.output
				version: "1.45"
			}

			shell: shellcheck.#Lint & {
				source: _website.output
			}

			markdown: markdownlint.#Lint & {
				source: _website.output
				files: ["./docs", "README.md"]
			}

			"cue": cue.#Lint & {
				source: _website.output
			}
		}
	}
}
