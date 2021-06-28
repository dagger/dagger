package testing

import "alpha.dagger.io/dagger/op"

TestMountTmpfs: {
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
				echo -n ok > /out
				echo -n ok > /tmpdir/out
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
