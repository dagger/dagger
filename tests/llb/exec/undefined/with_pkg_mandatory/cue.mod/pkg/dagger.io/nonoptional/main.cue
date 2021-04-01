package nonoptional

dang: string

#compute: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do:  "exec"
		dir: "/"
		args: ["sh", "-c", """
		echo "This test SHOULD fail, because this SHOULD be executed"
		exit 1
		"""]
	},
]
