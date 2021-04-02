package testing

X1=in: string

test: {
	string

	#up: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
				echo -n "received: \(X1)" > /out
				"""]
			// XXX Blocked by https://github.com/blocklayerhq/dagger/issues/19
			dir: "/"
		},
		{
			do:     "export"
			source: "/out"
			format: "string"
		},
	]
}
