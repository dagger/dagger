package testing

#up: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["sh", "-c", """
				echo "pwd is: $(pwd)"
				[ "$(pwd)" == "/etc" ] || exit 1
			"""]
		dir: "/etc"
	},
]
