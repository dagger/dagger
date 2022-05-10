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
	"github.com/dagger/dagger/ci/gitpod"
)

dagger.#Plan & {

	client: filesystem: ".": read: exclude: [
		"bin",
		"**/node_modules",
		"cmd/dagger/dagger",
		"cmd/dagger/dagger-debug",
		"website",
	]
	client: filesystem: "./bin": write: contents: actions.build."go".output
	client: network: "unix:///var/run/docker.sock": connect: dagger.#Socket

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
				os:      *client.platform.os | "linux"
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
		test: {
			unit: go.#Test & {
				// container: image: _goImage.output
				source:  _source
				package: "./..."

				command: flags: "-race": true
			}

			integration: bats.#Bats & {
				_daggerLinuxBin: go.#Build & {
					source:  _source
					package: "./cmd/dagger/"
					arch:    client.platform.arch
					container: command: flags: "-race": true
				}
				_testDir: core.#Subdir & {
					input: _source
					path:  "tests"
				}
				_mergeFS: core.#Merge & {
					inputs: [
						// directory containing integration tests
						_testDir.output,
						// dagger binary
						_daggerLinuxBin.output,
					]
				}
				env: DAGGER_BINARY: "/src/dagger"
				source: _mergeFS.output
				initScript: #"""
					# Remove the symlinked pkgs
					rm -rf cue.mod/pkg/*
					# Install sops
					# FIXME: should be in its own package
					curl -o /usr/bin/jq -L \
						https://github.com/stedolan/jq/releases/download/jq-1.6/jq-linux64 \
						&& chmod +x /usr/bin/jq
					curl -o /usr/bin/sops -L \
						https://github.com/mozilla/sops/releases/download/v3.7.2/sops-v3.7.2.linux \
						&& chmod +x /usr/bin/sops
					$DAGGER_BINARY project update
					"""#
				mounts: docker: {
					dest:     "/var/run/docker.sock"
					contents: client.network."unix:///var/run/docker.sock".connect
				}
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

		"gitpod": gitpod.#Test & {
			source: _source
		}
	}
}
