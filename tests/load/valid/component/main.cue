package testing

component: #compute: [{
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

	#compute: [
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

	#compute: [
		{
			do: "load"
			from: #compute: [{
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
