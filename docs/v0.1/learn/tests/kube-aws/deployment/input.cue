package main

import (
	"alpha.dagger.io/git"
)

manifest: git.#Repository & {
	remote: "https://github.com/dagger/examples.git"
	ref:    "main"
	subdir: "todoapp/k8s"
}

repository: git.#Repository & {
	remote: "https://github.com/dagger/examples.git"
	ref:    "main"
	subdir: "todoapp"
}

registry: "125635003186.dkr.ecr.\(awsConfig.region).amazonaws.com/dagger-ci"
