package testing

#up: [
	{
		do:  "fetch-container"
		ref: "alpine"
	},
	{
		do: "exec"
		args: ["sh", "-c", #"""
			echo "$foo"
			"""#]
		env: foo: lala: "lala"
	},
]
