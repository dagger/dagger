package testing

test1: {
	string

	#compute: [
		{
			do:  "fetch-container"
			ref: "busybox"
		},
		{
			do: "copy"
			from: [{do: "fetch-container", ref: "alpine"}]
			src:  "/etc/issue"
			dest: "/"
		},
		{
			do:     "export"
			source: "/issue"
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
			from: [{do: "fetch-container", ref: "busybox"}]
			src:  "/etc/issue"
			dest: "/"
		},
		{
			do:     "export"
			source: "/issue"
			format: "string"
		},
	]
}
