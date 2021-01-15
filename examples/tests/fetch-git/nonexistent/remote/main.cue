package testing

#dagger: {
	compute: [
		{
			do: "fetch-git"
			remote: "https://github.com/blocklayerhq/lalalala.git"
			ref: "master"
		}
	]
}
