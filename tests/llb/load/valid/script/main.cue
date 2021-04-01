package testing

test: {
	string

	#compute: [
		{
			do: "load"
			from: [{do: "fetch-container", ref: "alpine"}]
		},
		{
			do:     "export"
			source: "/etc/issue"
			format: "string"
		},
	]
}
