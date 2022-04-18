package python

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/docker"
	"universe.dagger.io/python"
)

dagger.#Plan & {
	actions: test: {

		// Run with a custom path to python
		customPath: {

			// python:3.10-alpine has both python3 and python in the PATH
			_image: docker.#Pull & {
				source: "python:3.10-alpine"
			}
			run: python.#Run & {
				input: _image.output
				command: name:    "python"
				script: contents: #"print("Hello, world!")"#
			}
			// This needs no output test because it is only testing that the command runs
		}

		// Run a script from source directory + filename
		runFile: {

			dir:   _load.output
			_load: core.#Source & {
				path: "./data"
				include: ["*.py"]
			}

			run: python.#Run & {
				export: files: "/out.txt": _
				script: {
					directory: dir
					filename:  "helloworld.py"
				}
			}
			output: run.export.files."/out.txt" & "Hello, world\n"
		}

		// Run a script from string
		runString: {
			run: python.#Run & {
				export: files: "/output.txt": _
				script: contents: #"""
					with open("output.txt", 'w') as f:
					    f.write("Hello, inlined world!\n")
					"""#
			}
			output: run.export.files."/output.txt" & "Hello, inlined world!\n"
		}
	}
}
