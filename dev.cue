package main

import (
	"dagger.io/dagger"
	"dagger.io/alpine"
)

// Dagger source code
source: dagger.#Artifact

// Build the dagger binaries
build: #Container & {
	image: #ImageFromRef & {ref: "docker.io/golang:1.16-alpine"}

	setup: [
		"apk add --no-cache file",
	]

	command: """
		go test -v ./...
		go build -o /binaries/ ./cmd/...
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
	outputDir: "/binaries"
}

// Execute `dagger help`
usage: #Container & {
	image: alpine.#Image

	command: "dagger help"

	volume: binaries: {
		from: build
		dest: "/usr/local/dagger/bin/"
	}
	shell: search: "/usr/local/dagger/bin": true
}
