package main

import (
	"alpha.dagger.io/git"
)

repository: git.#Repository & {
	remote: "https://github.com/dagger/examples.git"
	ref:    "main"
	subdir: "todoapp"
}
