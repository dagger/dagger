package def

#dang: string

#compute: [
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
