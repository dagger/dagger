package docker

import (
	"alpha.dagger.io/random"
)

suffix: random.#String & {
	seed: ""
}

app: #Run & {
	name: "daggerci-test-local-\(suffix.out)"
	ref:  "hello-world"
}
