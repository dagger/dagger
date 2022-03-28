package changelog

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	inputs: directories: app: path: "/Users/gerhard/github.com/thechangelog/changelog.com/"
}
