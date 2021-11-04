// Helpers to run bash commands in containers
package bash

import (
	"universe.dagger.io/docker"
)

// Run a bash command or script in a container
#Run: docker.#Run & {
	script: string
	cmd: {
		name: "bash"
		flags: {
			"-c":          script
			"--noprofile": true
			"--norc":      true
			"-e":          true
			"-o":          "pipefail"
		}
	}
}
