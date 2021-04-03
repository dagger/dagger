package main

import (
	"strings"

	"dagger.io/dagger"
	"dagger.io/alpine"
)

// Dagger source code
source: dagger.#Artifact

// Go environment
goenv: #Container & {
	image: #ImageFromRef & {
		ref: "docker.io/golang:1.16-alpine"
	}

	setup: "apk add --no-cache file"

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
	env: {
		GOMODCACHE:  volume.goCache.dest
		CGO_ENABLED: "0"

		let pathPrefixes = ["/", "/usr/", "/usr/local/", "/usr/local/go/"]
		PATH: strings.Join([
			for prefix in pathPrefixes {prefix + "sbin"},
			for prefix in pathPrefixes {prefix + "bin"},
		], ":")
	}
	command: {
		debug: {
			args: ["env"]
			always: true
		}
		test: {
			dir: "/src"
			args: ["go", "test", "-v", "/src/..."]
		}
		build: {
			dir: "/src"
			args: ["go", "build", "-o", "/binaries/", "/src/cmd/..."]
			outputDir: "/binaries"
		}
	}
}

runner: #Container & {
	image: alpine.#Image & {
		package: make: true
	}

	volume: daggerBinaries: {
		from: goenv.command.build
		dest: "/usr/local/dagger/bin"
	}
	env: PATH: "/bin:/usr/bin:/usr/local/bin:/usr/local/dagger/bin"

	command: {
		// Run `dagger help`
		usage: args: ["dagger", "help"]
		// FIXME: run integration tests

		// Just a debug command to check that this config works
		debug: {
			args: ["env"]
			env: FOO: "BAR"
		}
	}
}
