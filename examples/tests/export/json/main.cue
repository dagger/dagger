package testing

test: #dagger: compute: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["sh", "-c", """
			echo '{"something": "something"}' > /tmp/out
			""",
		]
		dir: "/"
	},
	{
		do: "export"
		// Source path in the container
		source: "/tmp/out"
		format: "json"
	},
]
