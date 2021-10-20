package testing

import "alpha.dagger.io/dagger/op"

X1=in: string

test: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
				echo -n "received: \(X1)" > /out
				"""]
			// XXX Blocked by https://github.com/blocklayerhq/dagger/issues/19
			dir: "/"
		},
		op.#Export & {
			source: "/out"
			format: "string"
		},
	]
}
