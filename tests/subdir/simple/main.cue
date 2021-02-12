package main

hello: {
	string

	#compute: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["mkdir", "-p", "/tmp/foo"]
		},
		{
			do: "exec"
			args: ["sh", "-c", "echo -n world > /tmp/foo/hello"]
		},
		{
			do:  "subdir"
			dir: "/tmp/foo"
		},
		{
			do:     "export"
			source: "/hello"
			format: "string"
		},
	]
}
