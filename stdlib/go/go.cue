package go

import (
	"dagger.io/dagger"
)

#Go: {
	// Go version to use
	version: *"1.16" | string

	// Arguments to the Go binary
	args: [...string]

	// Source Directory to build
	source: dagger.#Dir

	// Environment variables
	env: [string]: string

	#dagger: compute: [
		dagger.#FetchContainer & {
			ref: "docker.io/golang:\(version)-alpine"
		},
		dagger.#Exec & {
			"args": ["go"] + args

			"env": env
			"env": CGO_ENABLED: "0"

			dir: "/src"
			mount: "/src": from: source

			mount: "/root/.cache": "cache"
		},
	]
}

#Build: {
	// Go version to use
	// version: *#Go.version | string
	version: *"1.16" | string

	// Source Directory to build
	source: dagger.#Dir

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

	#dagger: compute: [
		dagger.#Copy & {
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
	source: dagger.#Dir

	// Packages to test
	packages: *"." | string

	#Go & {
		"version": version
		"source":  source
		args: ["test", "-v", packages]
	}
}
