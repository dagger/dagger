package docker

import (
	"alpha.dagger.io/random"
)

suffix: random.#String & {
	seed: ""
}

run: #Container & {
	name: "daggerci-test-local-\(suffix.out)"
	ref:  "hello-world"
}
