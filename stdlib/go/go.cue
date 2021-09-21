// Go build operations
package go

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/docker"

	"alpha.dagger.io/os"
)

// A standalone go environment
#Container: {

	// Go version to use
	version: *"1.16" | string & dagger.#Input

	// Source code
	source: dagger.#Artifact & dagger.#Input

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
	}
}

// Re-usable component for the Go compiler
#Go: {
	// Go version to use
	version: *"1.16" | string & dagger.#Input

	// Arguments to the Go binary
	args: [...string] & dagger.#Input

	// Source Directory to build
	source: dagger.#Artifact & dagger.#Input

	// Environment variables
	env: {
		[string]: string & dagger.#Input
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
	version: *#Go.version | string & dagger.#Input

	// Source Directory to build
	source: dagger.#Artifact & dagger.#Input

	// Packages to build
	packages: *"." | string & dagger.#Input

	// Target architecture
	arch: *"amd64" | string & dagger.#Input

	// Target OS
	os: *"linux" | string & dagger.#Input

	// Build tags to use for building
	tags: *"" | string & dagger.#Input

	// LDFLAGS to use for linking
	ldflags: *"" | string & dagger.#Input

	// Specify the targeted binary name
	output: string & dagger.#Output

	// Environment variables
	env: {
		[string]: string & dagger.#Input
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
	version: *#Go.version | string & dagger.#Input

	// Source Directory to build
	source: dagger.#Artifact & dagger.#Input

	// Packages to test
	packages: *"." | string & dagger.#Input

	#Go & {
		"version": version
		"source":  source
		args: ["test", "-v", packages]
	}
}
