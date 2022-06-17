package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"encoding/yaml"
)

// Define and run multi-container applications with Docker
#Compose: {
	source: dagger.#FS

	// Composefile definition or path into source
	composeFile: *{
		path:       string | *"docker-compose.yml"
		_fileName:  path
		_directory: source
	} | {
		contents:  _
		_fileName: "docker-compose.yml"
		_write:    core.#WriteFile & {
			input:      source
			path:       _filename
			"contents": yaml.Marshal(contents)
		}
		_directory: _write.output
	}

	// Arguments to the compose command
	args: [...string]

	// Command-line flags represented in a civilized form
	flags: [string]: (string | true)

	// Where in the container to mount the compose directory
	_mountPoint: "/src"

	#Run & {
		mounts: src: {
			dest:     _mountPoint
			contents: composeFile._directory
		}
		workdir: _mountPoint
		command: {
			name:    "compose"
			"flags": flags
			"args":  args
		}
	}
}
