package testing

test: {
	string

	#dagger: {
		compute: [
			{
				do: "load"
				from: [{do: "fetch-container", ref: "alpine"}]
			},
			{
				do: "exec"
				args: ["sh", "-c", """
					echo "NOT SURE WHAT TO TEST YET" > /out
				"""]
				dir: "/"
				mount: something: "cache"
			},
			{
				do: "export"
				source: "/out"
				format: "string"
			},
		]
	}
}
