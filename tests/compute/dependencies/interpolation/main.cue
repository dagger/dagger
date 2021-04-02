package testing

A: {
	result: string

	#up: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
				echo '{"result": "from A"}' > /tmp/out
				""",
			]
			dir: "/"
		},
		{
			do: "export"
			// Source path in the container
			source: "/tmp/out"
			format: "json"
		},
	]
}

B: {
	result: string

	#up: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
				echo "{\\"result\\": \\"dependency \(A.result)\\"}" > /tmp/out
				""",
			]
			dir: "/"
		},
		{
			do: "export"
			// Source path in the container
			source: "/tmp/out"
			format: "json"
		},
	]
}
