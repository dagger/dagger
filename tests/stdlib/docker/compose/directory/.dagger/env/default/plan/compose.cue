package compose

import (
	"dagger.io/dagger"
	"dagger.io/docker/compose"
)

repo: dagger.#Artifact

TestCompose: {
	up: compose.#Up & {
		ssh: {
			host: "143.198.64.230"
			user: "root"
		}
		context: repo
	}

	//verify: #VerifyCompose & {
	// ssh:  up.ssh
	// port: 8080
	//}

	cleanup: #CleanupCompose & {
		context: up
		ssh:     up.ssh
	}
}
