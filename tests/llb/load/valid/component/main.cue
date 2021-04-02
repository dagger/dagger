package testing

component: #up: [{
	do:  "fetch-container"
	ref: "alpine"
}, {
	do: "exec"
	args: ["sh", "-c", """
		printf lol > /id
		"""]
	dir: "/"
}]

test1: {
	string

	#up: [
		{
			do:   "load"
			from: component
		},
		{
			do:     "export"
			source: "/id"
			format: "string"
		},
	]
}

test2: {
	string

	#up: [
		{
			do: "load"
			from: #up: [{
				do:  "fetch-container"
				ref: "alpine"
			}, {
				do: "exec"
				args: ["sh", "-c", """
					printf lol > /id
					"""]
				dir: "/"
			}]
		},
		{
			do:     "export"
			source: "/id"
			format: "string"
		},
	]
}
