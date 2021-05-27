// A dagger workflow to develop dagger
package main

import (
	"dagger.io/dagger"
	"dagger.io/os"
	"dagger.io/go"
)

// Dagger source code
source: dagger.#Artifact @dagger(input)

test: {
	// Go unit tests
	unit: {
		logs: (os.#File & {
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

	binaries: os.#Dir & {
		from: ctr
		path: "/binaries"
	}

	logs: (os.#File & {
		from: ctr
		path: "/build.log"
		read: format: "string"
	}).read.data
}

// Execute `dagger help`
usage: os.#Container & {
	command: "dagger help"

	let binpath = "/usr/local/dagger/bin"
	mount: "\(binpath)": from:   build.binaries
	shell: search: "\(binpath)": true
}
