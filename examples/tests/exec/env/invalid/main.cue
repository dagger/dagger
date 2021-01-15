package testing

#dagger: compute: [
	{
		do: "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["sh", "-c", #"""
		echo "$foo"
		"""#]
		env: foo: {lala: "lala"}
		// XXX Blocked by https://github.com/blocklayerhq/dagger/issues/19
		dir: "/"
	},
]
