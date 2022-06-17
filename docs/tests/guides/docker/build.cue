// This action builds a docker image from a python app.
#PythonBuild: {
	// Source code of the Python application
	app: dagger.#FS

	_build: docker.#Build & {
		steps: [
			docker.#Pull & {
				source: "python:3.9"
			},
			docker.#Copy & {
				contents: app
				dest:     "/app"
			},
			docker.#Run & {
				command: {
					name: "pip"
					args: ["install", "-r", "/app/requirements.txt"]
				}
			},
			docker.#Set & {
				config: cmd: ["python", "/app/app.py"]
			},
		]
	}

	// Resulting container image
	image: _set.output
}
