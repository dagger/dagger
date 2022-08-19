// Use [NPM](https://www.npmjs.com/) in a Dagger action
package npm

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
)

// Install dependencies with npm ('npm install')
#Install: #Command & {
	args: ["install"]
}

// Build an application with npm ('npm run build')
#Build: {
	// Name of the project being build
	project: string | *"default"

	// App source code
	source: dagger.#FS

	// Output of the build
	output: dagger.#FS & script.output

	script: #Script & {
		// Name of the npm build script
		name:      *"build" | string
		"source":  source
		"project": project
	}
}

// Run a npm script ('npm run <NAME>')
#Script: {
	// App source code
	source: dagger.#FS

	// NPM project
	project: string

	// Name of the npm script to run
	// Example: "build"
	name: string

	#Command & {
		"source":  source
		"project": project
		args: ["run", name]

		// Mount output directory of install command,
		//   even though we don't need it,
		//   to trigger an explicit dependency.
		container: mounts: install_output: {
			contents: install.output
			dest:     "/tmp/npm_install_output"
		}
	}

	install: #Install & {
		"source":  source
		"project": project
	}

}

// Run a npm command (`npm <ARGS>')
#Command: {
	// Source code to build
	source: dagger.#FS

	// Arguments to npm
	args: [...string]

	// Project name, used for cache scoping
	project: string | *"default"

	// Path of the npm script's output directory
	// May be absolute, or relative to the workdir
	outputDir: string | *"./build"

	// Output directory
	output: container.export.directories."/output"

	// Logs produced by the npm script
	logs: container.export.files."/logs"

	container: bash.#Run & {
		"args": args

		input:  *_image.output | _
		_image: alpine.#Build & {
			packages: {
				bash: {}
				nodejs: {}
				npm: {}
				git: {}
			}
		}

		workdir: "/src"
		mounts: Source: {
			dest:     "/src"
			contents: source
		}
		script: contents: """
			set -x
			npm "$@" | tee /logs
			echo $$ > /code
			if [ -e "$NPM_OUTPUT_FOLDER" ]; then
				mv "$NPM_OUTPUT_FOLDER" /output
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
			NPM_CACHE_FOLDER:  "/cache/npm"
			NPM_OUTPUT_FOLDER: outputDir
		}
		mounts: {
			"NPM cache": {
				dest:     "/cache/npm"
				contents: core.#CacheDir & {
					id: "\(project)-npm"
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
