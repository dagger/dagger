// Yarn is a package manager for Javascript applications
package yarn

import (
	"strings"

	"alpha.dagger.io/dagger"
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/os"
)

// A Yarn package
#Package: {
	// Application source code
	source: dagger.#Artifact @dagger(input)

	// Extra alpine packages to install
	package: {
		[string]: true | false | string
	} @dagger(input)

	// working directory to use
	cwd: *"." | string @dagger(input)

	// Environment variables
	env: {
		[string]: string
	} @dagger(input)

	// Write the contents of `environment` to this file,
	// in the "envfile" format
	writeEnvFile: string | *"" @dagger(input)

	// Read build output from this directory
	// (path must be relative to working directory)
	buildDir: string | *"build" @dagger(input)

	// Run this yarn script
	script: string | *"build" @dagger(input)

	// Optional arguments for the script
	args: [...string] | *[] @dagger(input)

	// Build output directory
	build: os.#Dir & {
		from: ctr
		path: "/build"
	} @dagger(output)

	ctr: os.#Container & {
		image: alpine.#Image & {
			"package": package & {
				bash: "=~5.1"
				yarn: "=~1.22"
			}
		}
		shell: path: "/bin/bash"
		command: """
			[ -n "$ENVFILE_NAME" ] && echo "$ENVFILE" > "$ENVFILE_NAME"
			yarn --cwd "$YARN_CWD" install --production false

			opts=( $(echo $YARN_ARGS) )
			yarn --cwd "$YARN_CWD" run "$YARN_BUILD_SCRIPT" ${opts[@]}
			mv "$YARN_BUILD_DIRECTORY" /build
			"""
		"env": env & {
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
		dir: "/src"
		mount: "/src": from: source
		cache: "/cache/yarn": true
	}
}
