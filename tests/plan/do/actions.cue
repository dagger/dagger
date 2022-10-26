package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/yarn"
	"universe.dagger.io/docker"
)

dagger.#Plan & {

	// All the things!
	actions: {

		// Run core integration tests
		"core-integration": {}

		// Format all cue files
		cuefmt: {}

		// Lint and format all cue files
		cuelint: {}

		// Build a debug version of the dev dagger binary
		"dagger-debug": {}

		// Test docs
		"doc-test": {}

		// Generate docs
		docs: {}

		// Generate & lint docs
		docslint: {}

		// Run Europa universe tests
		"europa-universe-test": {}

		// Go lint
		golint: {}

		// Show how to get started & what targets are available
		help: {}

		// Install a dev dagger binary
		install: {}

		// Run all integration tests
		integration: {}

		// Lint everything
		lint: {}

		// Run shellcheck
		shellcheck: {}

		// Run all tests
		test: {}

		// Find all TODO items
		todo: {}

		// Run universe tests
		"universe-test": {}

		// Build, test and deploy frontend web client
		frontend: {
			// Build via yarn
			build: yarn.#Build & {
				source: dagger.#Scratch
			}

			// Test via headless browser
			test: docker.#Run & {
				input: docker.#Image
			}
		}
	}
}
