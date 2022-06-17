package python

import (
	"universe.dagger.io/docker"
)

// Python image default name
_#DefaultName: "python"

// Python image default repository
_#DefaultRepository: "index.docker.io"

// Python image default version
_#DefaultVersion: "3.10"

#Image: {
	name:       *_#DefaultName | string
	repository: *_#DefaultRepository | string
	version:    *_#DefaultVersion | string

	// Whether to use the alpine-based image or not
	alpine: *true | false

	docker.#Pull & {
		*{
			alpine: true
			source: "\(repository)/\(name):\(version)-alpine"
		} | {
			alpine: false
			source: "\(repository)/\(name):\(version)"
		}
	}
}
