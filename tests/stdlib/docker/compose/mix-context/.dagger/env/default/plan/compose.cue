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
		composeFile: #"""
			version: "3"

			services:
			  api:
			    build: .
			    environment:
			      PORT: 7000
			    ports:
			    - 7000:7000
			"""#
	}

	//verify: #VerifyCompose & {
	// ssh:  up.ssh
	// port: 7000
	//}

	cleanup: #CleanupCompose & {
		context: up
		ssh:     up.ssh
	}
}
