// Yarn is a package manager for Javascript applications
package yarn

import (
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/engine" // FIXME: should not be needed for common cases

	"universe.dagger.io/alpine"
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

	// Optional arguments for the script
	args: [...string] | *[]

	// Secret variables
	secrets: [string]: dagger.#Secret

	// Yarn version
	yarnVersion: *"=~1.22" | string

	// Run yarn in a containerized build environment
	command: bash.#Run & {
		*{
			image: (alpine.#Build & {
				bash: version: "=~5.1"
				yarn: version: yarnVersion
			}).image
			env: CUSTOM_IMAGE: "0"
		} | {
			env: CUSTOM_IMAGE: "1"
		}

		script: """
			# Create $ENVFILE_NAME file if set
			[ -n "$ENVFILE_NAME" ] && echo "$ENVFILE" > "$ENVFILE_NAME"

			yarn --cwd "$YARN_CWD" install --production false

			opts=( $(echo $YARN_ARGS) )
			yarn --cwd "$YARN_CWD" run "$YARN_BUILD_SCRIPT" ${opts[@]}
			mv "$YARN_BUILD_DIRECTORY" /build
			"""

		mounts: {
			"yarn cache": {
				dest:     "/cache/yarn"
				contents: engine.#CacheDir
			}
			"package source": {
				dest:     "/src"
				contents: source
			}
			// FIXME: mount secrets
		}

		output: directories: "/build": _

		env: {
			YARN_BUILD_SCRIPT:    script
			YARN_ARGS:            strings.Join(args, "\n")
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

	// The final contents of the package after build
	output: command.output.directories."/build".contents
}
