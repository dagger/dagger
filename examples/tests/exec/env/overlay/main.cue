package testing

bar: string

#dagger: compute: [
	{
		do: "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["sh", "-c", """
			echo "foo: $foo"
			[ "$foo" == "overlay environment" ] || exit 1
		"""]
		env: foo: bar
		// XXX Blocked by https://github.com/blocklayerhq/dagger/issues/19
		dir: "/"
	},
]
