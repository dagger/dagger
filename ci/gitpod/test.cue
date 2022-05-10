package gitpod

import (
	"dagger.io/dagger"

	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
)

// This plan ensures the Gitpod image builds and scripts
// specified in the Gitpod config are executed successfully.
#Test: {
	source: dagger.#FS

	_image: docker.#Dockerfile & {
		"source": source

		dockerfile: path: ".gitpod.Dockerfile"
	}

	bash.#Run & {
		input: _image.output
		mounts: "source": {
			contents: source
			dest:     "/src"
			ro:       false
		}
		workdir: "/src"
		script: contents: """
			# Create Go cache dir
			sudo mkdir -p /workspace/go && sudo chown gitpod:gitpod /workspace/go

			# Read the shell script from config and execute it
			yq ".tasks[0].init" .gitpod.yml | bash -
			
			# Ensure the expected tools were installed
			make install
			cue version
			dagger version
			"""
	}
}
