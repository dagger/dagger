package markdownlint

import (
	"dagger.io/dagger"

	"universe.dagger.io/docker"
)

#Lint: {
	// Source code
	source: dagger.#FS

	// shellcheck version
	version: *"0.31.1" | string

	// Files to lint
	files: [...string]

	_image: docker.#Pull & {
		source: "tmknom/markdownlint:\(version)"
	}

	container: docker.#Run & {
		input: _image.output
		mounts: "source": {
			dest:     "/src"
			contents: source
		}
		workdir: "/src"
		command: {
			// FIXME: this should not be required
			name: "markdownlint"
			args: files
		}
	}
}
