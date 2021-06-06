package compose

import (
	"dagger.io/git"
	"dagger.io/docker/compose"
)

TestCompose: {
	up: compose.#Up & {
		ssh: {
			host: "143.198.64.230"
			user: "root"
		}
		context: git.#Repository & {
			remote: "https://github.com/dagger/examples"
			ref:    "main"
			subdir: "voteapp"
		}
	}

	//verifyApp: #VerifyCompose & {
	// ssh:  up.ssh
	// port: 5000
	//}

	//verifyResult: #VerifyCompose & {
	// ssh:  up.ssh
	// port: 5001
	//}

	cleanup: #CleanupCompose & {
		context: up
		ssh:     up.ssh
	}
}
