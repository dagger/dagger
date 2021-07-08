package def

#dang: string

#up: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do:  "exec"
		dir: "/"
		args: ["sh", "-c", """
			echo success
			"""]
	},
]
