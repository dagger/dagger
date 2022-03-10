import (
	"encoding/yaml"
	// ...
)

dagger.#Plan & {
	client: filesystem: "config.yaml": write: {
		// Convert a CUE value into a YAML formatted string
		contents: yaml.Marshal(actions.pull.output.config)
	}

	actions: pull: docker.#Pull & {
		source: "alpine"
	}
}
