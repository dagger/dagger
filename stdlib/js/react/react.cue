package react

import (
	"dagger.io/dagger"
	"dagger.io/alpine"
	"dagger.io/docker"
)

// A ReactJS application
#App: {
	// Application source code
	source: dagger.#Artifact

	// Yarn-specific settings
	yarn: {
		// Read build output from this directory
		// (path must be relative to working directory).
		buildDir: string | *"build"

		// Run this yarn script
		script: string | *"build"
	}
	setup: [
		"mkdir -p /cache/yarn",
	]

	// Build the application in a container, using yarn
	build: docker.#Container & {
		image: alpine.#Image & {
			package: bash: "=~5.1"
			package: yarn: "=~1.22"
		}
		dir:     "/src"
		command: """
			yarn install --production false
			yarn run "$YARN_BUILD_SCRIPT"
			mv "$YARN_BUILD_DIRECTORY" \(outputDir)
			"""
		volume: {
			src: {
				from: source
				dest: "/src"
			}
			//   yarnCache: {
			//    type: "cache"
			//    dest: "/cache/yarn"
			//   }
		}
		outputDir: "/build"
		shell: {
			path: "/bin/bash"
			args: [
				"--noprofile",
				"--norc",
				"-eo", "pipefail",
				"-c",
			]
		}
		env: {
			YARN_BUILD_SCRIPT:    yarn.script
			YARN_CACHE_FOLDER:    "/cache/yarn"
			YARN_BUILD_DIRECTORY: yarn.buildDir
		}
	}

}
