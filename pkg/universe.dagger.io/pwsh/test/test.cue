package pwsh

import (
	"dagger.io/dagger"

	"universe.dagger.io/docker"
	"universe.dagger.io/pwsh"
)

dagger.#Plan & {
	actions: test: {

		_pull: docker.#Pull & {
			source: "mcr.microsoft.com/powershell"
		}
		_image: _pull.output

		// Run a script from source directory + filename
		runFile: {

			dir:   _load.output
			_load: dagger.#Source & {
				path: "./data"
				include: ["*.sh"]
			}

			run: pwsh.#Run & {
				input: _image
				export: files: "/out.txt": _
				script: {
					directory: dir
					filename:  "hello.psq"
				}
			}
			output: run.export.files."/out.txt" & "Hello world!\n"
		}

		// Run a script from string
		runString: {
			run: bash.#Run & {
				input: _image
				export: files: "/output.txt": _
				script: contents: "Set-Content -Value 'Hello inline world!' -Path '/output.txt'"
			}
			output: run.export.files."/output.txt" & "Hello inline world!\n"
		}

	}
}
