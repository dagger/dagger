package testcore

import (
	"dagger.io/dagger"
)

name: dagger.#Input & {
	string | *"world"
}

message: "Hello, \(name)!" @dagger(output)

dir: dagger.#Input & dagger.#Artifact

samedir: dir @dagger(output)
