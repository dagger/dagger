package testing

#dagger: compute: [
	{
		do: "fetch-container"
		ref: "busybox"
	}
]

#dagger: compute: [
	{
		do: "fetch-container"
		ref: "alpine"
	}
]
