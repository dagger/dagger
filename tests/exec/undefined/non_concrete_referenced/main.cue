package testing

hello: "world"
bar:   string

#dagger: compute: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do:  "exec"
		dir: "/"
		args: ["sh", "-c", """
		echo \(bar)
		echo "This test SHOULD succeed, because this is never going to be executed, as \(bar) is not concrete"
		exit 1
		"""]
	},
]
