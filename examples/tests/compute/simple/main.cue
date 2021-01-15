package testing

#dagger: {
	compute: [
		{
			do: "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["true"]
			dir: "/"
		}
	]
}
