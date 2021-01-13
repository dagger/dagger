package test

#dagger: compute: [
	{
		do: "fetch-container"
		ref: "busybox"
	},
	{
		do: "exec"
		args: ["echo", "foo"]
		dir: "/"
		always: true
	},
]
