package testing

hello: "world"

bar: string

#dagger: compute: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do:  "exec"
		dir: "/"
		args: ["sh", "-c", "echo \(bar)"]
	},
]
