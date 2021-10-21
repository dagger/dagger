package testing

import "alpha.dagger.io/dagger/op"

TestExportScalar: {
	bool

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
				echo true > /tmp/out
				""",
			]
		},
		op.#Export & {
			// Source path in the container
			source: "/tmp/out"
			format: "json"
		},
	]
}

TestExportMap: #up: [
	op.#FetchContainer & {
		ref: "alpine"
	},
	op.#Exec & {
		args: ["sh", "-c", """
			echo '{"something": "something"}' > /tmp/out
			""",
		]
	},
	op.#Export & {
		// Source path in the container
		source: "/tmp/out"
		format: "json"
	},
]

TestExportList: {
	[...string]

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
				echo '["milk", "pumpkin pie", "eggs", "juice"]' > /tmp/out
				""",
			]
		},
		op.#Export & {
			// Source path in the container
			source: "/tmp/out"
			format: "json"
		},
	]
}
