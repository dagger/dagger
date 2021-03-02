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
					cat /mnt/test/lol > /out
				"""]
			mount: "/mnt/test": {
				from: #dagger: compute: [
					{
						do:  "fetch-container"
						ref: "alpine"
					},
					{
						do: "exec"
						args: ["sh", "-c", """
							echo -n "hello world" > /lol
							"""]
					}
				]
			}
		},
		{
			do:     "export"
			source: "/out"
			format: "string"
		},
	]
}
