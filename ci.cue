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
	client: env: {
		DAGGER_LOG_FORMAT:             string | *"auto"
		OTEL_EXPORTER_JAEGER_ENDPOINT: string | *""
		JAEGER_TRACE:                  string | *""
		BUILDKIT_HOST:                 string | *""
		DAGGER_CACHE_FROM:             string | *""
		DAGGER_CACHE_TO:               string | *""
		GITHUB_ACTIONS:                string | *""
		ACTIONS_RUNTIME_TOKEN:         string | *""
		ACTIONS_CACHE_URL:             string | *""
		TESTDIR:                       string | *"."
	}

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

		test: {
			// Go unit tests
			unit: go.#Test & {
				source:  _source
				package: "./..."

				command: flags: "-race": true
				env: DAGGER_LOG_FORMAT: client.env.DAGGER_LOG_FORMAT
			}

			#BatsIntegrationTest: {
				// Directory containing the basts files
				path: string

				// dagger binary
				daggerBinary: _

				_testDir: core.#Subdir & {
					input:  _source
					"path": path
				}
				_mergeFS: core.#Merge & {
					inputs: [
						// directory containing integration tests
						_testDir.output,
						// dagger binary
						daggerBinary.output,
					]
				}

				bats.#Bats & {
					env: {
						DAGGER_BINARY:                 "/src/dagger"
						DAGGER_LOG_FORMAT:             client.env.DAGGER_LOG_FORMAT
						BUILDKIT_HOST:                 client.env.BUILDKIT_HOST
						OTEL_EXPORTER_JAEGER_ENDPOINT: client.env.OTEL_EXPORTER_JAEGER_ENDPOINT
						JAEGER_TRACE:                  client.env.JAEGER_TRACE
						DAGGER_CACHE_FROM:             client.env.DAGGER_CACHE_FROM
						DAGGER_CACHE_TO:               client.env.DAGGER_CACHE_TO
						GITHUB_ACTIONS:                client.env.GITHUB_ACTIONS
						ACTIONS_RUNTIME_TOKEN:         client.env.ACTIONS_RUNTIME_TOKEN
						ACTIONS_CACHE_URL:             client.env.ACTIONS_CACHE_URL
					}
					source: _mergeFS.output
					initScript: #"""
						set -exu
						[ -d cue.mod/pkg/ ] && {
							# Remove the symlinked pkgs
							rm -rf cue.mod/pkg/*
							$DAGGER_BINARY project update
						}
						# Install sops
						# FIXME: should be in its own package
						curl -o /usr/bin/jq -sL \
							https://github.com/stedolan/jq/releases/download/jq-1.6/jq-linux64 \
							&& chmod +x /usr/bin/jq
						curl -o /usr/bin/sops -sL \
							https://github.com/mozilla/sops/releases/download/v3.7.2/sops-v3.7.2.linux \
							&& chmod +x /usr/bin/sops
						"""#
					mounts: docker: {
						dest:     "/var/run/docker.sock"
						contents: client.network."unix:///var/run/docker.sock".connect
					}
				}
			}

			integration: {
				core: #BatsIntegrationTest & {
					path:         "tests"
					daggerBinary: go.#Build & {
						source:  _source
						package: "./cmd/dagger/"
						arch:    client.platform.arch
						container: command: flags: "-race": true
					}
				}
				// FIXME: docs integration tests were never ported after the Europa release (gh issue #2592)
				// doc: #BatsIntegrationTest & {
				//  path:         "docs/learn/tests"
				//  daggerBinary: build.go & {os: "linux"}
				// }
				universe: #BatsIntegrationTest & {
					path:         "pkg"
					daggerBinary: build.go & {os: "linux"}
					testDir:      "universe.dagger.io"
					env: TESTDIR: client.env.TESTDIR
					extraArgs: "$(find ${TESTDIR:-.} -type f -name '*.bats' -not -path '*/node_modules/*' -not -path '*/cue.mod/*' -not -path '*/x/*')"
				}
				experimental: #BatsIntegrationTest & {
					path:         "pkg"
					daggerBinary: build.go & {os: "linux"}
					testDir:      "universe.dagger.io/x"
					env: TESTDIR: client.env.TESTDIR
					extraArgs: "$(find ${TESTDIR:-.} -type f -name '*.bats' -not -path '*/node_modules/*' -not -path '*/cue.mod/*')"
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
