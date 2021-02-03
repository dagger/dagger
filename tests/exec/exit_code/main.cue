package testing

#dagger: compute: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["sh", "-c", "exit 123"]
		// XXX Blocked by https://github.com/blocklayerhq/dagger/issues/19
		dir: "/"
	},
]
