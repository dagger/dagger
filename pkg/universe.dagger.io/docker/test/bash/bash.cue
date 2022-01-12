package docker

cmd: #Command & {
	script: "echo hello world"
	exec: {
		name: "/bin/bash"
		flags: {
			"-c":          script
			"--noprofile": true
			"--norc":      true
			"-e":          true
			"-o":          "pipefail"
		}
	}
}
