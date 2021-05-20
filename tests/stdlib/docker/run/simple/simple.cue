package docker

import (
	"dagger.io/docker"
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/alpine"
)

// Run with --input-file key=$HOME/.ssh/<your private server key>
key: dagger.#Artifact

TestRun: {
	random: {
		string
		#up: [
			op.#Load & {from: alpine.#Image},
			op.#Exec & {
				always: true
				args: ["sh", "-c", "cat /dev/urandom | tr -dc 'a-z' | fold -w 10 | head -n 1 | tr -d '\n' > /rand"]
			},
			op.#Export & {
				source: "/rand"
			},
		]
	}

	run: docker.#Run & {
		host:  "143.198.64.230"
		ref:   "nginx:alpine"
		user:  "root"
		name:  "daggerci-test-simple-\(random)"
		"key": key
	}
}
