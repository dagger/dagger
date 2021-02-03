package testing

#dagger: compute: [
	{
		do:     "fetch-git"
		remote: "https://github.com/blocklayerhq/acme-clothing.git"
		ref:    "master"
	},
]
