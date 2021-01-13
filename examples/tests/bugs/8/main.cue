package test

www: {
	host: "lalal"

	source: {

		string
		#dagger: compute: [
			{
				do: "fetch-container"
				ref: "alpine"
			},
			{
				do: "exec"
				args: ["sh", "-c", """
					echo \(host) > /tmp/out
				"""]
				dir: "/"
			},
			{
				do: "export"
				source: "/tmp/out"
				format: "string"
			}
		]
	}
}
