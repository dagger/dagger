// Yarn is a package manager for Javascript applications
package yarn

import (
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
)

#Build: #Run & {
	buildDir: *"build" | string
	script:   *"build" | string
}

// Run a Yarn command
#Run: {
	// Custom name for this command.
	// Assign an app-specific name if there are multiple apps in the same plan.
	name: string | *""

	// App source code
	source: dagger.#FS

	// Working directory to use
	cwd: *"." | string

	// Write the contents of `environment` to this file, in the "envfile" format
	writeEnvFile: string | *""

	// Optional: Read build output from this directory
	// Must be relative to working directory, cwd
	buildDir?: string

	// Yarn script to run for this command.
	script: string

	// Fix for shadowing issues
	let yarnScript = script

	// Optional arguments for the script
	args: [...string] | *[]

	// Secret variables
	// FIXME: not implemented. Are they needed?
	secrets: [string]: dagger.#Secret

	// FIXME: this is a weird hack. We should remove it.
	container: #input: docker.#Image | *{
		// FIXME: Yarn's version depends on Alpine's version
		// Yarn version
		// yarnVersion: *"=~1.22" | string
		// FIXME: custom base image not supported
		alpine.#Build & {
			packages: {
				bash: {}
				yarn: {}
			}
		}
	}

	_loadScripts: core.#Source & {
		path: "."
		include: ["*.sh"]
	}

	_run: docker.#Build & {
		steps: [
			container.#input,

			docker.#Copy & {
				dest:     "/src"
				contents: source
			},

			bash.#Run & {
				script: {
					directory: _loadScripts.output
					filename:  "install.sh"
				}
				mounts: "yarn cache": {
					dest:     "/cache/yarn"
					contents: core.#CacheDir & {
						// FIXME: are there character limitations in cache ID?
						id: "universe.dagger.io/yarn.#Run \(name)"
					}
				}

				env: {
					YARN_CACHE_FOLDER: "/cache/yarn"
					YARN_CWD:          cwd
				}

				workdir: "/src"
			},

			bash.#Run & {
				script: {
					directory: _loadScripts.output
					filename:  "run.sh"
				}

				mounts: "yarn cache": {
					dest:     "/cache/yarn"
					contents: core.#CacheDir & {
						// FIXME: are there character limitations in cache ID?
						id: "universe.dagger.io/yarn.#Run \(name)"
					}
				}

				env: {
					YARN_BUILD_SCRIPT: yarnScript
					YARN_ARGS:         strings.Join(args, "\n")
					YARN_CACHE_FOLDER: "/cache/yarn"
					YARN_CWD:          cwd
					if buildDir != _|_ {
						YARN_BUILD_DIRECTORY: buildDir
					}
					if writeEnvFile != "" {
						ENVFILE_NAME: writeEnvFile
						ENVFILE:      strings.Join([ for k, v in env {"\(k)=\(v)"}], "\n")
					}
				}

				workdir: "/src"
			},
		]
	}

	// The final contents of the package after run
	_output: core.#Subdir & {
		input: _run.output.rootfs
		path:  "/build"
	}

	output: _output.output
}
