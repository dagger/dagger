package testing

import "dagger.io/dagger/op"

test: {
	string

	#up: [
		op.#Load & {
			from: [
				op.#FetchContainer & {
					ref: "alpine"
				},
			]
		},
		op.#Exec & {
			args: ["sh", "-c", """
				echo ok > /out
				echo ok > /tmpdir/out
				"""]
			mount: "/tmpdir": "tmpfs"
		},
		op.#Exec & {
			args: ["sh", "-c", """
				[ -f /out ] || exit 1
				# content of /cache/tmp must not exist in this layer
				[ ! -f /tmpdir/out ] || exit 1
				"""]
		},
		op.#Export & {
			source: "/out"
			format: "string"
		},
	]
}
