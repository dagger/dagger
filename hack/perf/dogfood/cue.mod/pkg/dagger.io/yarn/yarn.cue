package yarn

import (
	"dagger.io/dagger"
	"dagger.io/alpine"
)

// Yarn Script
#Script: {
	// Source code of the javascript application
	source: dagger.#Dir

	// Run this yarn script
	run: string | *"build"

	// Read build output from this directory
	// (path must be relative to working directory).
	buildDirectory: string | *"build"

	// Set these environment variables during the build
	env?: [string]: string

	#dagger: compute: [
		dagger.#Load & {
			from: alpine.#Image & {
				package: bash: "=5.1.0-r0"
				package: yarn: "=1.22.10-r0"
			}
		},
		dagger.#Exec & {
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"-c",
				"""
					yarn install --production false
					yarn run "$YARN_BUILD_SCRIPT"
					mv "$YARN_BUILD_DIRECTORY" /build
					""",
			]
			if env != _|_ {
				"env": env
			}
			"env": {
				YARN_BUILD_SCRIPT:    run
				YARN_CACHE_FOLDER:    "/cache/yarn"
				YARN_BUILD_DIRECTORY: buildDirectory
			}
			dir: "/src"
			mount: {
				"/src": from: source
				"/cache/yarn": "cache"
			}
		},
		dagger.#Subdir & {
			dir: "/build"
		},
	]
}
