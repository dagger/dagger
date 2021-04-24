package testing

import "dagger.io/dagger/op"

#TestContainer: #up: [
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
		dir: "/tmp/foo"
	},
]

TestSubdirMount: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},

		// Check number of file in /source (should contains only hello)
		op.#Exec & {
			args: ["sh", "-c", "test $(ls /source | wc -l) = 1"]
			mount: "/source": from: #TestContainer
		},

		op.#Exec & {
			args: ["sh", "-c", "cat /source/hello > /out"]
			mount: "/source": from: #TestContainer
		},

		op.#Export & {
			source: "/out"
			format: "string"
		},
	]
}
