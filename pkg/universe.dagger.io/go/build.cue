package go

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
)

// Build a go binary
#Build: {
	// Go version to use
	version: *#Image.version | string

	// Source code
	source: dagger.#FS

	// Target package to build
	package: *"." | string

	// Target architecture
	arch: *"amd64" | string

	// Target OS
	os: *"linux" | string

	// Build tags to use for building
	tags: *"" | string

	// LDFLAGS to use for linking
	ldflags: *"" | string

	// Target binary output
	output: string

	env: [string]: string

	_build: #Container & {
		"version": version
		"source":  source
		"env":     env
		args: ["build", "-v", "-tags", tags, "-ldflags", ldflags, "-o", output, package]
	}

	_copy: engine.#Copy & {
		input:    engine.#Scratch
		contents: _build.output.rootfs
		source:   output
		dest:     output
	}

	binary: _copy.output
}
