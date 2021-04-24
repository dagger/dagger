package testing

TestExportInvalidPath: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Export & {
			// Source path in the container
			source: "/tmp/lalala"
			format: "string"
		},
	]
}
