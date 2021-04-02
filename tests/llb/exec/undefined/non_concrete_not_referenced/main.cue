package testing

hello: "world"
bar:   string

#up: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do:  "exec"
		dir: "/"
		args: ["sh", "-c", """
		echo \(hello)
		echo "This test SHOULD fail, because this script SHOULD execute, since bar is not referenced"
		exit 1
		"""]
	},
]
