// Yarn is a package manager for Javascript applications
package yarn

import (
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/engine"

	"universe.dagger.io/docker"
	"universe.dagger.io/bash"
)

// Build a Yarn package
#Build: {
	// Application source code
	source: dagger.#FS

	// working directory to use
	cwd: *"." | string

	// Write the contents of `environment` to this file,
	// in the "envfile" format
	writeEnvFile: string | *""

	// Read build output from this directory
	// (path must be relative to working directory)
	buildDir: string | *"build"

	// Run this yarn script
	script: string | *"build"

	// Fix for shadowing issues
	let yarnScript = script

	// Cache to use, passed by the caller
	cache: engine.#CacheDir

	// Optional arguments for the script
	args: [...string] | *[]

	// FIXME: Yarn's version depends on Alpine's version
	// Yarn version
	// yarnVersion: *"=~1.22" | string

	// Run yarn in a containerized build environment
	run: bash.#Run & {
		"args": args

		_loadScripts: dagger.#Source & {
			path: "."
			include: ["*.sh"]
		}
		"source": _loadScripts.output
		filename: "yarn-build.sh"

		container: {
			// FIXME: allow swapping out image
			image: _installYarn.output
			// FIXME use 'alpine' package
			// FIXME: why does this not work in outer scope?
			_base: docker.#Pull & {
				source: "alpine"
			}
			_installYarn: docker.#Run & {
				input: _base.output
				command: {
					name: "apk"
					args: ["add", "bash", "yarn"]
					flags: {
						"-U":         true
						"--no-cache": true
					}
				}
			}

			mounts: {
				"yarn cache": {
					dest:     "/cache/yarn"
					contents: cache
				}
				"package source": {
					dest:     "/src"
					contents: source
				}
			}

			export: directories: "/build": _

			env: {
				YARN_BUILD_SCRIPT:    yarnScript
				YARN_CACHE_FOLDER:    "/cache/yarn"
				YARN_CWD:             cwd
				YARN_BUILD_DIRECTORY: buildDir
				if writeEnvFile != "" {
					ENVFILE_NAME: writeEnvFile
					ENVFILE:      strings.Join([ for k, v in env {"\(k)=\(v)"}], "\n")
				}
			}

			workdir: "/src"
		}
	}

	// The final contents of the package after build
	output: run.container.export.directories."/build".contents
}
