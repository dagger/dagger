package testcore

import (
	"dagger.io/dagger"
)

name: string | *"world" @dagger(input)
message: "Hello, \(name)!" @dagger(output)

dir: dagger.#Artifact @dagger(input)
samedir: dir @dagger(output)
