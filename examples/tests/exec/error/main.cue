package testing

#dagger: compute: [
	{
		do: "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["erroringout"]
		// XXX Blocked by https://github.com/blocklayerhq/dagger/issues/19
		dir: "/"
	},
]
