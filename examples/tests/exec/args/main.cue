package testing

list: #dagger: compute: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["echo", "output"]
		// XXX Blocked by https://github.com/blocklayerhq/dagger/issues/19
		dir:    "/"
		always: true
	},
]

str: #dagger: compute: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: "echo output"
		// XXX Blocked by https://github.com/blocklayerhq/dagger/issues/19
		dir:    "/"
		always: true
	},
]
