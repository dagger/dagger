package main

import (
	"dagger.io/dagger"

	"universe.dagger.io/bash"
	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
	"universe.dagger.io/go"

	"github.com/dagger/dagger/ci/golangci"
	"github.com/dagger/dagger/ci/shellcheck"
	"github.com/dagger/dagger/ci/markdownlint"
)

dagger.#Plan & {

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

			cue: docker.#Build & {
				// FIXME: spin off into its own package?
				steps: [
					alpine.#Build & {
						packages: bash: _
						packages: curl: _
						packages: git:  _
					},

					docker.#Copy & {
						contents: _source
						source:   "go.mod"
						dest:     "go.mod"
					},

					// Install CUE
					bash.#Run & {
						script: contents: #"""
								export CUE_VERSION="$(grep cue ./go.mod | cut -d' ' -f2 | head -1 | sed -E 's/\.[[:digit:]]\.[[:alnum:]]+-[[:alnum:]]+$//')"
								export CUE_TARBALL="cue_${CUE_VERSION}_linux_amd64.tar.gz"
								echo "Installing cue version $CUE_VERSION"
								curl -L "https://github.com/cue-lang/cue/releases/download/${CUE_VERSION}/${CUE_TARBALL}" | tar zxf - -C /usr/local/bin
								cue version
						"""#
					},

					// CACHE: copy only *.cue files
					docker.#Copy & {
						contents: _source
						include: ["*.cue"]
						dest: "/cue"
					},

					// LINT
					bash.#Run & {
						workdir: "/cue"
						script: contents: #"""
							find . -name '*.cue' -not -path '*/cue.mod/*' -print | time xargs -t -n 1 -P 8 cue fmt -s
							test -z "$(git status -s . | grep -e "^ M"  | grep "\.cue" | cut -d ' ' -f3 | tee /dev/stderr)"
							"""#
					},
				]
			}
		}
	}
}
