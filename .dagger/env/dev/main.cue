// A dagger workflow to develop dagger
package main

import (
	"dagger.io/dagger"
	"dagger.io/io"
	"dagger.io/alpine"
	"dagger.io/docker"
	"dagger.io/go"
)

// Dagger source code
source: dagger.#Artifact


test: {
	// Go unit tests
	unit: {
		logs: (io.#File & {
			from: build.ctr
			path: "/test.log"
			read: format: "string"
		}).read.data
	}

	// Full suite of bats integration tests
	integration: {
		// FIXME
	}
}

// Build the dagger binaries
build: {
	ctr: go.#Container & {
		"source": source
		setup: [
			"apk add --no-cache file",
		]
		command: """
			go test -v ./... > /test.log
			go build -o /binaries/ ./cmd/... > /build.log
			"""
	}

	binaries: docker.#Container & {
		image: ctr
		outputDir: "/binaries"
	}
}


// Execute `dagger help`
usage: docker.#Container & {
	image: alpine.#Image

	command: "dagger help"

	volume: binaries: {
		from: build.binaries
		dest: "/usr/local/dagger/bin/"
	}
	shell: search: "/usr/local/dagger/bin": true
}
