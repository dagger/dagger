package testing

teststring: {
	string

	#compute: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
				echo something > /tmp/out
				""",
			]
		},
		{
			do: "export"
			// Source path in the container
			source: "/tmp/out"
			format: "lalalalal"
		},
	]
}
