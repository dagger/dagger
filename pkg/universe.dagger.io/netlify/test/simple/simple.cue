package netlify

import (
	"dagger.io/dagger"
)

deploy: #Deploy & {
	contents: dagger.#Scratch
}
