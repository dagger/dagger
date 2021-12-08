package testing

import (
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
)

TestData: dagger.#Artifact

_expected: """
	/src/b.txt
	
	/src/foo:
	bar.txt
	
	"""

TestIgnore: {
	string
	#up: [
		op.#Load & {from: alpine.#Image},
		op.#Exec & {
			args: ["sh", "-c", "ls /src/* > /out.txt"]
			mount: "/src": from: TestData
		},
		op.#Export & {source: "/out.txt"},
		op.#Exec & {
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
