// Go build operations
package go

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/docker"

	"dagger.io/os"
)

// A standalone go environment
#Container: {

	// Go version to use
	version: *"1.16" | string @dagger(input)

	// Source code
	source:  dagger.#Artifact @dagger(input)

	os.#Container & {
		env: CGO_ENABLED: "0"

		image: docker.#Pull & {
			from: "docker.io/golang:\(version)-alpine"
		}

		// Setup source dir
		let srcPath = "/src"
		mount: "\(srcPath)": from: source
		dir: srcPath

		// Setup go cache
		let cachePath = "/root/.cache/gocache"
		cache: "\(cachePath)": true
		env: GOMODCACHE:       cachePath

		// Add go to search path (FIXME: should be inherited from image metadata)
		shell: search: "/usr/local/go/bin": true
	}
}

// Re-usable component for the Go compiler
#Go: {
	// Go version to use
	version: *"1.16" | string @dagger(input)

	// Arguments to the Go binary
	args: [...string] @dagger(input)

	// Source Directory to build
	source: dagger.#Artifact @dagger(input)

	// Environment variables
	env: {
		[string]: string @dagger(input)
	}

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

// Go application builder
#Build: {
	// Go version to use
	version: *#Go.version | string @dagger(input)

	// Source Directory to build
	source: dagger.#Artifact @dagger(input)

	// Packages to build
	packages: *"." | string @dagger(input)

	// Target architecture
	arch: *"amd64" | string @dagger(input)

	// Target OS
	os: *"linux" | string @dagger(input)

	// Build tags to use for building
	tags: *"" | string @dagger(input)

	// LDFLAGS to use for linking
	ldflags: *"" | string @dagger(input)

	// Specify the targeted binary name
	output: string @dagger(output)

	// Environment variables
	env: {
		[string]: string @dagger(input)
	}

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
	version: *#Go.version | string @dagger(input)

	// Source Directory to build
	source: dagger.#Artifact @dagger(input)

	// Packages to test
	packages: *"." | string @dagger(input)

	#Go & {
		"version": version
		"source":  source
		args: ["test", "-v", packages]
	}
}
