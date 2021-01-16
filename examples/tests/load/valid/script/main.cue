package testing


test: {
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
