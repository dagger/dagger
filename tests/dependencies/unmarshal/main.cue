package testing

import "encoding/json"

A: {
	string

	#compute: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
				echo '{"hello": "world"}' > /tmp/out
				""",
			]
			dir: "/"
		},
		{
			do: "export"
			// Source path in the container
			source: "/tmp/out"
			format: "string"
		},
	]
}

unmarshalled: json.Unmarshal(A)

B: {
	result: string

	#compute: [
		{
			do:  "fetch-container"
			ref: "alpine"
		},
		{
			do: "exec"
			args: ["sh", "-c", """
				echo "{\\"result\\": \\"unmarshalled.hello=\(unmarshalled.hello)\\"}" > /tmp/out
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
