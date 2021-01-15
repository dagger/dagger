package testing

teststring: {
	string

	#dagger: {
		compute: [
			{
				do: "fetch-container"
				ref: "alpine"
			},
			{
				do: "export"
				// Source path in the container
				source: "/tmp/lalala"
				format: "string"
			},
		]
	}
}
