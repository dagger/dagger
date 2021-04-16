package main

import (
	"dagger.io/alpine"
	"dagger.io/dagger/op"
)

rand: {
	string

	#up: [
		op.#Load & {from: alpine.#Image},
		op.#Exec & {
			always: true
			args: ["sh", "-c", """
				tr -dc A-Za-z0-9 </dev/urandom | head -c 13 > /id
				"""]
		},
		op.#Export & {source: "/id"},
	]
}

// If rand is executed twice, cue won't validate
ref1: rand
ref2: ref1
