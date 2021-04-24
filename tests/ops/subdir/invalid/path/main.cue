package testing

import "dagger.io/dagger/op"

TestInvalidPathSubdir: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["mkdir", "-p", "/tmp/foo"]
		},
		op.#Exec & {
			args: ["sh", "-c", "echo -n world > /tmp/foo/hello"]
		},
		op.#Subdir & {
			dir: "/directorynotfound"
		},
		op.#Export & {
			source: "./hello"
			format: "string"
		},
	]
}
