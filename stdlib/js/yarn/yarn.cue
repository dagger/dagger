// Yarn is a package manager for Javascript applications
// https://yarnpkg.com
package yarn

import (
	"strings"

	"dagger.io/dagger"
	"dagger.io/alpine"
	"dagger.io/os"
)

// A Yarn package.
#Package: {
	// Application source code
	source: dagger.#Artifact @dagger(input)

	// Environment variables
	env: {
		[string]: string @dagger(input)
	}

	// Write the contents of `environment` to this file,
	// in the "envfile" format.
	writeEnvFile: string | *"" @dagger(input)

	// Read build output from this directory
	// (path must be relative to working directory).
	buildDir: string | *"build" @dagger(input)

	// Run this yarn script
	script: string | *"build" @dagger(input)

	build: os.#Dir & {
		from: ctr
		path: "/build"
	} @dagger(output)

	ctr: os.#Container & {
		image: alpine.#Image & {
			package: {
				bash: "=~5.1"
				yarn: "=~1.22"
			}
		}
		shell: path: "/bin/bash"
		command: """
			[ -n "$ENVFILE_NAME" ] && echo "$ENVFILE" > "$ENVFILE_NAME"
			yarn install --production false
			yarn run "$YARN_BUILD_SCRIPT"
			mv "$YARN_BUILD_DIRECTORY" /build
			"""
		"env": env & {
			YARN_BUILD_SCRIPT:    script
			YARN_CACHE_FOLDER:    "/cache/yarn"
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
