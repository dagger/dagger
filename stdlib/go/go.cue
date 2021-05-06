package go

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"

	"dagger.io/os"
)

// A standalone go environment
#Container: {
	// Go version to use
	version: *"1.16" | string
	source:  dagger.#Artifact

	os.#Container & {
		env: {
			GOMODCACHE:  volume.goCache.dest
			CGO_ENABLED: "0"
		}

		from: "docker.io/golang:\(version)-alpine"

		volume: {
			goSource: {
				from: source
				dest: "/src"
			}
			goCache: {
				type: "cache"
				dest: "/root/.cache/gocache"
			}
		}

		dir: volume.goSource.dest
		// Add go to search path (FIXME: should be inherited from image metadata)
		shell: search: "/usr/local/go/bin": true
	}
}

#Go: {
	// Go version to use
	version: *"1.16" | string

	// Arguments to the Go binary
	args: [...string]

	// Source Directory to build
	source: dagger.#Artifact

	// Environment variables
	env: [string]: string

	#up: [
		op.#FetchContainer & {
			ref: "docker.io/golang:\(version)-alpine"
		},
		op.#Exec & {
			"args": ["go"] + args

			"env": env
			"env": CGO_ENABLED: "0"
			"env": GOMODCACHE:  "/root/.cache/gocache"

			dir: "/src"
			mount: "/src": from: source

			mount: "/root/.cache": "cache"
		},
	]
}

#Build: {
	// Go version to use
	version: *#Go.version | string

	// Source Directory to build
	source: dagger.#Artifact

	// Packages to build
	packages: *"." | string

	// Target architecture
	arch: *"amd64" | string

	// Target OS
	os: *"linux" | string

	// Build tags to use for building
	tags: *"" | string

	// LDFLAGS to use for linking
	ldflags: *"" | string

	// Specify the targeted binary name
	output: string

	env: [string]: string

	#up: [
		op.#Copy & {
			from: #Go & {
				"version": version
				"source":  source
				"env":     env
				args: ["build", "-v", "-tags", tags, "-ldflags", ldflags, "-o", output, packages]
			}
			src:  output
			dest: output
		},
	]
}

#Test: {
	// Go version to use
	version: *#Go.version | string

	// Source Directory to build
	source: dagger.#Artifact

	// Packages to test
	packages: *"." | string

	#Go & {
		"version": version
		"source":  source
		args: ["test", "-v", packages]
	}
}
