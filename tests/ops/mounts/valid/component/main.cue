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
					cat /mnt/test/lol > /out
				"""]
			mount: "/mnt/test": from: #up: [
				op.#FetchContainer & {
					ref: "alpine"
				},
				op.#Exec & {
					args: ["sh", "-c", """
						echo -n "hello world" > /lol
						"""]
				},
			]
		},
		op.#Export & {
			source: "/out"
			format: "string"
		},
	]
}
