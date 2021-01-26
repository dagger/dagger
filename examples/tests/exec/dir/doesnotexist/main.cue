package testing

#dagger: compute: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["sh", "-c", """
				echo "pwd is: $(pwd)"
				[ "$(pwd)" == "/thisisnonexistent" ] || exit 1
			"""]
		dir: "/thisisnonexistent"
	},
]
