package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

// Modular build API for Docker containers
#Build: {
	steps: [#Step, ...#Step]
	output: #Image

	// Generate build DAG from linear steps
	_dag: {
		for idx, step in steps {
			"\(idx)": step & {
				// connect input to previous output
				if idx > 0 {
					// FIXME: the intermediary `output` is needed because of a possible CUE bug.
					// `._dag."0".output: 1 errors in empty disjunction::`
					// See: https://github.com/cue-lang/cue/issues/1446
					// input: _dag["\(idx-1)"].output
					_output: _dag["\(idx-1)"].output
					input:   _output
				}
			}
		}
	}

	if len(_dag) > 0 {
		output: _dag["\(len(_dag)-1)"].output
	}
}

// A build step is anything that produces a docker image
#Step: {
	input?: #Image
	output: #Image
	...
}

// Build step that copies files into the container image
#Copy: {
	input:    #Image
	contents: dagger.#FS
	source:   string | *"/"
	dest:     string | *"/"
	// Optionally include certain files
	include: [...string]
	// Optionally exclude certain files
	exclude: [...string]

	// Execute copy operation
	_copy: core.#Copy & {
		"input":    input.rootfs
		"contents": contents
		"source":   source
		"dest":     dest
		"include":  include
		"exclude":  exclude
	}

	output: #Image & {
		config: input.config
		rootfs: _copy.output
	}
}

// Build step that executes a Dockerfile
#Dockerfile: {
	source: dagger.#FS

	// Dockerfile definition or path into source
	dockerfile: *{
		path: string | *"Dockerfile"
	} | {
		contents: string
	}

	// Registry authentication
	// Key must be registry address
	auth: [registry=string]: {
		username: string
		secret:   dagger.#Secret
	}

	platforms: [...string]
	target?: string
	buildArg: [string]: string
	label: [string]:    string
	hosts: [string]:    string

	_build: core.#Dockerfile & {
		"source":     source
		"auth":       auth
		"dockerfile": dockerfile
		"platforms":  platforms
		if target != _|_ {
			"target": target
		}
		"buildArg": buildArg
		"label":    label
		"hosts":    hosts
	}

	output: #Image & {
		rootfs: _build.output
		config: _build.config
	}
}
