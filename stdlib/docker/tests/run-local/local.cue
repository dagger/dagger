package docker

import (
	"dagger.io/random"
)

suffix: random.#String & {
	seed: ""
}

run: #Run & {
	name: "daggerci-test-local-\(suffix.out)"
	ref:  "hello-world"
}
