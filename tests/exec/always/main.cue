package testing

#compute: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["echo", "always output"]
		always: true
	},
]
