package testing

import (
	"alpha.dagger.io/dagger"
)

first: dagger.#Input & {bool | *false}

if first == true {
	second: true
}
