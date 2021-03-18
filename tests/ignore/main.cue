package test

import (
	"dagger.io/alpine"
	"dagger.io/dagger"
	"dagger.io/llb"
)

TestData: dagger.#Artifact

_expected: """
	/src/b.txt
	
	/src/foo:
	bar.txt
	
	"""

TestIgnore: {
	string
	#compute: [
		llb.#Load & {from: alpine.#Image},
		llb.#Exec & {
			args: ["sh", "-c", "ls /src/* > /out.txt"]
			mount: "/src": from: TestData
		},
		llb.#Export & {source: "/out.txt"},
		llb.#Exec & {
			args: [
				"sh",
				"-ec",
				"""
				cat > /test.txt << EOF
				\(_expected)
				EOF
				test "$(cat /out.txt)" = "$(cat /test.txt)"
				""",
			]
		},
	]
}
