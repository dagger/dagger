package testing

component: #dagger: compute: [{
	do: "fetch-container"
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

	#dagger: {
		compute: [
			{
				do: "load"
				from: component
			},
			{
				do: "export"
				source: "/id"
				format: "string"
			},
		]
	}
}

test2: {
	string

	#dagger: {
		compute: [
			{
				do: "load"
				from: #dagger: compute: [{
					do: "fetch-container"
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
				do: "export"
				source: "/id"
				format: "string"
			},
		]
	}
}
