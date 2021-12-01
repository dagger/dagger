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
			package: bash: "=~5.1"
		}
	},

	op.#Exec & {
		mount: "/secret": secret: mySecret
		args: [
			"/bin/bash",
			"--noprofile",
			"--norc",
			"-eo",
			"pipefail",
			"-c",
			#"""
				test "$(cat /secret)" = "SecretValue"
				"""#,
		]
	},
]
