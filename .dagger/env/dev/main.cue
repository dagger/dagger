// A dagger workflow to develop dagger
package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/alpine"
	"dagger.io/docker"
)

// Dagger source code
source: dagger.#Artifact

// Build the dagger binaries
build: {
	testLogs: {
		string
		#up: [
			op.#Load & {
				from: ctr
			},
			op.#Exec & {
				args: ["ls", "-l", "/"]
			},
			op.#Export & {
				source: "/test.log"
				format: "string"
			}
		]
	}

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
