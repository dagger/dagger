// Use [Yarn](https://yarnpkg.com) in a Dagger action
package yarn

import (
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
)

#Build: #Run & {
	args: *["run", "build"] | _
}

// Run a yarn command or script
#Run: {
	// Source code to build
	source: dagger.#FS

	// Arguments to yarn
	args: [...string]

	// Project name, used for cache scoping
	project: string | *"default"

	// Path of the yarn script's output directory
	// May be absolute, or relative to the workdir
	outputDir: string | *"./build"

	// Output directory
	output: container.export.directories."/output"

	// Logs produced by the yarn script
	logs: container.export.files."/logs"

	// Other actions required to run before this one
	requires: [...string]

	// Yarn exit code.
	code: container.export.files."/code"

	container: bash.#Run & {
		input:  _image.output
		_image: alpine.#Build & {
			packages: {
				bash: {}
				yarn: {}
				git: {}
			}
		}

		"args":  args
		workdir: "/src"
		mounts: Source: {
			dest:     "/src"
			contents: source
		}
		script: contents: """
			set -x
			yarn "$@" | tee /logs
			echo $$ > /code
			if [ -e "$YARN_OUTPUT_FOLDER" ]; then
				mv "$YARN_OUTPUT_FOLDER" /output
			else
				mkdir /output
			fi
			"""
		export: {
			directories: "/output": dagger.#FS
			files: {
				"/logs": string
				"/code": string
			}
		}

		// Setup caching
		env: {
			YARN_CACHE_FOLDER:  "/cache/yarn"
			YARN_OUTPUT_FOLDER: outputDir
			REQUIRES:           "\(strings.Join(requires, "_"))"
		}
		mounts: {
			"Yarn cache": {
				dest:     "/cache/yarn"
				contents: core.#CacheDir & {
					id: "\(project)-yarn"
				}
			}
			"NodeJS cache": {
				dest:     "/src/node_modules"
				type:     "cache"
				contents: core.#CacheDir & {
					id: "\(project)-nodejs"
				}
			}
		}
	}
}
