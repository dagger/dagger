package testing

#dagger: compute: [
	{
		do: "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["sh", "-c", """
		[ "$foo" == "output environment" ] || exit 1
		"""]
		env: foo: "output environment"
		// XXX Blocked by https://github.com/blocklayerhq/dagger/issues/19
		dir: "/"
	},
]
