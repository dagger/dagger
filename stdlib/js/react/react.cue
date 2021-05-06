package react

import (
	"strings"

	"dagger.io/dagger"
	"dagger.io/alpine"
	"dagger.io/os"
)

// A ReactJS application
// FIXME: move this to a 'yarn' package for clarity
#App: {
	// Application source code
	source: dagger.#Artifact

	// Environment variables
	env: [string]: string

	// Write the contents of `environment` to this file,
	// in the "envfile" format.
	writeEnvFile: string | *""

	// Yarn-specific settings
	yarn: {
		// Read build output from this directory
		// (path must be relative to working directory).
		buildDir: string | *"build"

		// Run this yarn script
		script: string | *"build"
	}

	build: os.#Dir & {
		from: ctr
		path: "/build"
	}

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
			YARN_BUILD_SCRIPT:    yarn.script
			YARN_CACHE_FOLDER:    "/cache/yarn"
			YARN_BUILD_DIRECTORY: yarn.buildDir
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
