package goreleaser

import (
	"universe.dagger.io/docker"
)

// Default image registry
_#DefaultImageRegistry: "index.docker.io"

// Default image repository
_#DefaultImageRepository: "goreleaser/goreleaser"

// Default image tag
_#DefaultImageTag: "latest"

// GoReleaser image
#Image: {
	repository: string | *"\(_#DefaultImageRegistry)/\(_#DefaultImageRepository)"
	tag:        string | *_#DefaultImageTag

	docker.#Pull & {
		source: "\(repository):\(tag)"
	}
}
