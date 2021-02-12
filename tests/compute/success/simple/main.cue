package testing

#compute: [
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
