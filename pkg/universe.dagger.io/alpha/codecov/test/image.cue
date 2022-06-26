package codecov

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpha/codecov"
)

dagger.#Plan & {
	actions: test: codecov.#Image & {}
}
