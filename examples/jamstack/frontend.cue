package main

import (
	"dagger.io/dagger"
	"dagger.io/netlify"
	"dagger.io/js/react"
)

frontend: {
	// Source code to build the app
	source: dagger.#Artifact

	writeEnvFile?: string

	// Yarn Build
	yarn: {
		// Run this yarn script
		script: string | *"build"

		// Read build output from this directory
		// (path must be relative to working directory).
		buildDir: string | *"build"
	}

	// Build environment variables
	environment: [string]: string
	environment: NODE_ENV: string | *"production"
	environment: APP_URL:  "https://\(name).netlify.app/"
}

frontend: {
	app: react.#App & {
		source: frontend.source
		env:    frontend.environment

		if frontend.writeEnvFile != _|_ {
			writeEnvFile: frontend.writeEnvFile
		}

		yarn: {
			script:   frontend.yarn.script
			buildDir: frontend.yarn.buildDir
		}
	}

	// Host the application with Netlify
	site: netlify.#Site & {
		"name":   name
		account:  infra.netlifyAccount
		contents: app.build
	}
}
