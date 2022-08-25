package jreleaser

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

// Base
#Container: {
	// --== Public ==--

	// Source code
	source: dagger.#FS

	// JReleaser home path
	jreleaser_home?: dagger.#FS

	// JReleaser version
	version: string | *"latest"

	// JReleaser command to be executed
	cmd: string

	// Additional command arguments
	args: [...string]

	// Additional command flags
	flags: [string]: (string | true)

	// Environment variables
	env: [string]: string | dagger.#Secret
	_env: {
		JRELEASER_USER_HOME: "/.jreleaser"
	}

	// --== Private ==--

	_image: #Image & {
		"version": version
	}

	_sourcePath: "/workspace"

	docker.#Run & {
		input:   *_image.output | docker.#Image
		workdir: _sourcePath
		command: {
			name:    cmd
			"args":  args
			"flags": flags & {
				"--output-directory": "/out"
			}
		}

		// Defensive copy
		for k, v in env {
			_env: "\(k)": v
		}
		"env": _env

		mounts: {
			"source": {
				dest:     _sourcePath
				contents: source
			}

			if jreleaser_home != _|_ {
				"jreleaser_home": {
					dest:     "/.jreleaser"
					contents: jreleaser_home
				}
			}
		}
	}
}
