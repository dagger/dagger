package python

// custom image built with a pip3 package
#CustomImage: docker.#Build & {
	steps: [
		alpine.#Build & {
			packages: {
				bash: version: "=~5.1"
				"py3-pip":     _
				"python3-dev": _
			}
		},
		bash.#Run & {
			script: contents: "pip3 install ANY_PACKAGE"
		},
	]
}
