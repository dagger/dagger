package npm

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/docker"
	"universe.dagger.io/git"
)

// Tests for the npm package, grouped together in a reusable action.
#Tests: {
	// Test data, packaged alongside this cue file
	data: {
		contents: _load.output

		_load: core.#Source & {
			path: "./data/foo"
		}
	}

	// Run npm.#Build
	simple: {
		build: #Build & {
			source: data.contents
		}

		verify: #AssertFile & {
			input:    build.output
			path:     "test"
			contents: "output\n"
		}
	}

	// Run npm.#Build with a custom project name
	customName: {
		build: #Build & {
			project: "My Build"
			source:  data.contents
		}
		verify: #AssertFile & {
			input:    build.output
			path:     "test"
			contents: "output\n"
		}
	}

	// Build mdn/todo-react
	todoreact: {
		pull: git.#Pull & {
			remote: "https://github.com/mdn/todo-react"
			ref:    "4c1ad2bc5d50f96265693be50997c306081b0964"
		}
		install: #Install & {
			source: pull.output
		}
		build: {
			// A warning about eslint causes the build to fail unless we have this .env file
			env: core.#WriteFile & {
				input:    pull.output
				path:     "./.env"
				contents: "SKIP_PREFLIGHT_CHECK=true"
			}
			run: #Script & {
				source: env.output
				name:   "build"
			}
			output: run.output
		}
		verify: #AssertFile & {
			input: build.output
			path:  "robots.txt"
			contents: """
				# https://www.robotstxt.org/robotstxt.html
				User-agent: *
				Disallow:

				"""
		}
	}

	// Run npm.#Build with a custom docker image
	customImage: {
		buildImage: docker.#Build & {
			steps: [
				docker.#Pull & {
					source: "alpine"
				},
				docker.#Run & {
					command: {
						name: "apk"
						args: ["add", "nodejs", "npm", "bash"]
					}
				},
			]
		}

		image: build.output

		build: #Build & {
			source: data.contents
			script: container: input: buildImage.output
		}
	}
}

// Make an assertion on the contents of a file
#AssertFile: {
	input:    dagger.#FS
	path:     string
	contents: string

	_read: core.#ReadFile & {
		"input": input
		"path":  path
	}

	actual: _read.contents

	// Assertion
	contents: actual
}

dagger.#Plan & {
	actions: test: #Tests
}
