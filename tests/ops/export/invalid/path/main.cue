package testing

teststring: {
	string

	#up: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "export"
			// Source path in the container
			source: "/tmp/lalala"
			format: "string"
		},
	]
}
