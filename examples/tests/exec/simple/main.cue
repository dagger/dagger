package testing

#dagger: compute: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["echo", "simple output"]
		// XXX Blocked by https://github.com/blocklayerhq/dagger/issues/19
		dir: "/"
	},
]
