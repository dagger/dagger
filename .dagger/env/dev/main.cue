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
	unit: {
		logs: (io.#File & {
			from: build.ctr
			path: "/test.log"
			read: format: "string"
		}).read.data
	}
	integration: {
		// FIXME
	}
}

// Build the dagger binaries
build: {
	ctr: docker.#Container & {
		image: docker.#ImageFromRegistry & {
			ref: "docker.io/golang:1.16-alpine"
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
