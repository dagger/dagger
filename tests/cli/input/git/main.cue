package testing

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/os"
)

// Input https://github.com/dagger/examples/tree/main/todoapp
TestRepo: dagger.#Input & {dagger.#Artifact}

// Check README.md
TestFolder: os.#Container & {
	always: true
	command: #"""
			grep -q "Todo APP" /input/repo/README.md
		"""#
	mount: "/input/repo": from: TestRepo
}
