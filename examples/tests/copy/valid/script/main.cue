package testing

test: {
	string

	#dagger: {
		compute: [
			{
				do: "fetch-container"
				ref: "busybox"
			},
			{
				do: "copy"
				from: [{do: "fetch-container", ref: "busybox"}]
				src: "/etc/issue"
				dest: "/"
			},
			{
				do: "export"
				source: "/issue"
				format: "string"
			},
		]
	}
}
