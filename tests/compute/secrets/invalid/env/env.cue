package testing

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
	"dagger.io/alpine"
)

mySecret: dagger.#Secret

TestSecrets: #up: [
	op.#Load & {
		from: alpine.#Image & {
			package: bash: "=~5.1"
		}
	},

	op.#Exec & {
		env: foo: mySecret
	},
]
