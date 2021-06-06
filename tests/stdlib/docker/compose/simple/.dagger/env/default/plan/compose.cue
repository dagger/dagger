package compose

import (
	"dagger.io/docker/compose"
)

TestCompose: {
	up: compose.#Up & {
		ssh: {
			host: "143.198.64.230"
			user: "root"
		}
		composeFile: #"""
			version: "3"

			services:
			  nginx:
			    image: nginx:alpine
			    ports:
			    - 8080:80
			"""#
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
