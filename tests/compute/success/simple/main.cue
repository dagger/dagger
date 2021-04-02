package testing

#up: [
	{
		do:  "fetch-container"
		ref: "busybox"
	},
	{
		do: "exec"
		args: ["true"]
		dir: "/"
	},
]
