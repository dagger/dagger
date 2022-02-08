// Helpers to run bash commands in containers
package bash

import (
	"universe.dagger.io/docker"
)

// Run a bash command or script in a container
#Run: {
	// Contents of the bash script
	script: string

	// FIXME: don't pass the script as argument: write to filesystme instead
	docker.#Run & {
		command: {
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
}
