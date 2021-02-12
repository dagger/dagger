package testing

test: {
	string

	#compute: [
		{
			do: "load"
			from: [{do: "fetch-container", ref: "alpine"}]
		},
		{
			do: "exec"
			args: ["sh", "-c", """
					cat /mnt/test/lol > /out
				"""]
			mount: "/mnt/test": from: {
				#compute: [
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
