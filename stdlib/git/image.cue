package git

import (
	"alpha.dagger.io/alpine"
)

#Image: alpine.#Image & {
	package: {
		git: true
	}
}
