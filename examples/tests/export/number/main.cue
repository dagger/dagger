package testing

test: {
	number

	#dagger: compute: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
				echo -123.5 > /tmp/out
				""",
			]
		},
		{
			do: "export"
			// Source path in the container
			source: "/tmp/out"
			format: "number"
		},
	]
}
