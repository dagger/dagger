package pulumi

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
)

TestData: dagger.#Artifact @dagger(input)

TestPulumi: {
	apply: #Configuration & {
		source: TestData
		stack: "test"
		runtime: "nodejs"
	}
}
