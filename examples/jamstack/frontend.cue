package main

import (
	"dagger.io/dagger"
	"dagger.io/netlify"
	"dagger.io/js/yarn"
	"dagger.io/git"
)

frontend: {
	// Source code to build the app
	source: git.#Repository | dagger.#Artifact @dagger(input)

	writeEnvFile?: string @dagger(input)

	// Yarn Build
	yarn: {
		// Run this yarn script
		script: string | *"build" @dagger(input)

		// Read build output from this directory
		// (path must be relative to working directory).
		buildDir: string | *"build" @dagger(input)
	}

	// Build environment variables
	environment: {
		[string]: string @dagger(input)
	}
	environment: {
		NODE_ENV: string | *"production" @dagger(input)
	}
	environment: {
		APP_URL: "https://\(name).netlify.app/" @dagger(input)
	}
}

frontend: {
	app: yarn.#Package & {
		source: frontend.source
		env:    frontend.environment

		if frontend.writeEnvFile != _|_ {
			writeEnvFile: frontend.writeEnvFile
		}

		script:   frontend.yarn.script
		buildDir: frontend.yarn.buildDir
	}

	// Host the application with Netlify
	site: netlify.#Site & {
		"name":   name
		account:  infra.netlifyAccount
		contents: app.build
	}
}
