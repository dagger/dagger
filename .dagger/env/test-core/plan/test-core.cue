package testcore

import (
	"dagger.io/dagger"
)

name: dagger.#Input & {
	string | *"world"
}

message: dagger.#Output & "Hello, \(name)!"

dir: dagger.#Input & dagger.#Artifact

samedir: dagger.#Output & dir
