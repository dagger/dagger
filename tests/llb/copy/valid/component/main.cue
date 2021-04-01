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
			do:  "fetch-container"
			ref: "busybox"
		},
		{
			do:   "copy"
			from: component
			src:  "/id"
			dest: "/"
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
			do:  "fetch-container"
			ref: "busybox"
		},
		{
			do: "copy"
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
			src:  "/id"
			dest: "/"
		},
		{
			do:     "export"
			source: "/id"
			format: "string"
		},
	]
}
