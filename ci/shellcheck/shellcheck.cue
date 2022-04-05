package shellcheck

import (
	"dagger.io/dagger"

	"universe.dagger.io/docker"
)

#Lint: {
	// Source code
	source: dagger.#FS

	// shellcheck version
	version: *"0.8.0" | string

	_image: docker.#Pull & {
		source: "koalaman/shellcheck-alpine:v\(version)"
	}

	docker.#Run & {
		input: _image.output
		mounts: "source": {
			dest:     "/src"
			contents: source
		}
		workdir: "/src"
		command: {
			name: "sh"
			args: ["-c", #"""
				shellcheck $(find . -type f \( -iname \*.bats -o -iname \*.bash -o -iname \*.sh \) -not -path "*/node_modules/*" -not -path "*/bats-*/*")
				"""#]
		}
	}
}
