package testing

test: {
	string

	#up: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
				printf something > /tmp/out
				""",
			]
		},
		{
			do: "export"
			// Source path in the container
			source: "/tmp/out"
			format: "string"
		},
	]
}
