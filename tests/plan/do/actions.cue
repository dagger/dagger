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
		"core-integration": _

		// Format all cue files
		cuefmt: _

		// Lint and format all cue files
		cuelint: _

		// Build a debug version of the dev dagger binary
		"dagger-debug": _

		// Test docs
		"doc-test": _

		// Generate docs
		docs: _

		// Generate & lint docs
		docslint: _

		// Run Europa universe tests
		"europa-universe-test": _

		// Go lint
		golint: _

		// Show how to get started & what targets are available
		help: _

		// Install a dev dagger binary
		install: _

		// Run all integration tests
		integration: _

		// Lint everything
		lint: _

		// Run shellcheck
		shellcheck: _

		// Run all tests
		test: _

		// Find all TODO items
		todo: _

		// Run universe tests
		"universe-test": _

		// Build, test and deploy frontend web client
		frontend: {
			// Build via yarn
			build: yarn.#Build

			// Test via headless browser
			test: docker.#Run
		}
	}
}
