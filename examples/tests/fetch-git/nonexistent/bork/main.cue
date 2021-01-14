package testing

#dagger: {
	compute: [
		{
			do: "fetch-git"
			remote: "pork://pork"
			ref: "master"
		}
	]
}
