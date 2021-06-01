package docker

import (
	"dagger.io/dagger/op"
	"dagger.io/alpine"
)

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
