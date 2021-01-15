package testing

#dagger: compute: [
	{
		do: "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["sh", "-c", "echo should not succeed"]
		dir: "/thisisnonexistent"
	},
]
