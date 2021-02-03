package testing

test: {
	string

	#dagger: compute: [
		{
			do: "load"
			from: [{do: "fetch-container", ref: "alpine"}]
		},
		{
			do: "exec"
			args: ["sh", "-c", """
					ls -lA /lol > /out
				"""]
			dir: "/"
			mount: something: {
				input: dagger: #compute: [{
					do:  "fetch-container"
					ref: "alpine"
				}]
				path: "/lol"
			}
		},
		{
			do:     "export"
			source: "/out"
			format: "string"
		},
	]
}
