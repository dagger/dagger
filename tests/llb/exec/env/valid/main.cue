package testing

#up: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["sh", "-c", """
			[ "$foo" == "output environment" ] || exit 1
			"""]
		env: foo: "output environment"
	},
]
