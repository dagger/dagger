package main

import (
	"encoding/yaml"
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	client: filesystem: "config.yaml": write: contents: yaml.Marshal(actions.pull.image.config)
	actions: pull: docker.#Pull & {
		source: "alpine"
	}
}
