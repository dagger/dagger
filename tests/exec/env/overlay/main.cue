package testing

bar: string

#compute: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["sh", "-c", """
				echo "foo: $foo"
				[ "$foo" == "overlay environment" ] || exit 1
			"""]
		env: foo: bar
	},
]
