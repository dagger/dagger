package testing

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
)

mySecret: dagger.#Secret

TestSecrets: #up: [
	op.#Load & {
		from: alpine.#Image & {
			package: bash: true
		}
	},

	op.#Exec & {
		env: foo: mySecret
	},
]
