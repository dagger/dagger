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
		mount: "/secret": secret: mySecret
		env: PLAIN: mySecret.id
		args: [
			"/bin/bash",
			"--noprofile",
			"--norc",
			"-eo",
			"pipefail",
			"-c",
			#"""
				test "$(cat /secret)" = "SecretValue"
				test "$PLAIN" != "SecretValue"
				"""#,
		]
	},
]
