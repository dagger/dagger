package testing

teststring: {
	string

	#dagger: compute: [
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
			// XXX Blocked by https://github.com/blocklayerhq/dagger/issues/19
			dir: "/"
		},
		{
			do: "export"
			// Source path in the container
			source: "/tmp/out"
			format: "lalalalal"
		},
	]
}
