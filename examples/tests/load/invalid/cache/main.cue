package testing

test1: {
	string

	#dagger: {
		compute: [
			{
				do: "load"
				from: [{do: "fetch-container", ref: "alpine"}]
			},
			{
				do: "export"
				source: "/etc/issue"
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
				from: [{do: "fetch-container", ref: "busybox"}]
			},
			{
				do: "export"
				source: "/etc/issue"
				format: "string"
			},
		]
	}
}
