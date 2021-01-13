package test

#dagger: compute: [
	{
		do: "fetch-container"
		ref: "busybox"
	},
	{
		do: "copy"
		from: [
			{
				do: "fetch-container"
				ref: "alpine"
			},
		]
		dest: "/copied"
	},
]
