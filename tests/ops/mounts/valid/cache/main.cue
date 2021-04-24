package testing

import "dagger.io/dagger/op"

TestMountCache: {
	string

	#up: [
		op.#Load & {
			from: [{do: "fetch-container", ref: "alpine"}]
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo -n "NOT SURE WHAT TO TEST YET" > /out
				"""]
			dir: "/"
			mount: something: "cache"
		},
		op.#Export & {
			source: "/out"
			format: "string"
		},
	]
}
