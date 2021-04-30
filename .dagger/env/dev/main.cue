// A dagger workflow to develop dagger
package main

import (
	"dagger.io/dagger"
	"dagger.io/io"
	"dagger.io/alpine"
	"dagger.io/docker"
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
	ctr: docker.#Container & {
		image: docker.#ImageFromRegistry & {
			ref: "docker.io/golang:1.16-alpine@\(digest)"
			// FIXME: this digest is arch-specific (amd64)
			let digest="sha256:6600d9933c681cb38c13c2218b474050e6a9a288ac62bdb23aee13bc6dedce18"
		}

		setup: [
			"apk add --no-cache file",
		]

		command: """
			go test -v ./... > /test.log
			go build -o /binaries/ ./cmd/... > /build.log
			"""

		volume: {
			daggerSource: {
				from: source
				dest: "/src"
			}
			goCache: {
				type: "cache"
				dest: "/root/.cache/gocache"
			}
		}

		// Add go to search path (FIXME: should be inherited from image metadata)
		shell: search: "/usr/local/go/bin": true

		env: {
			GOMODCACHE:  volume.goCache.dest
			CGO_ENABLED: "0"
		}

		dir:       "/src"
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
