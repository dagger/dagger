package docker

import (
	"dagger.io/dagger"
	"universe.dagger.io/nginx"
)

tests: {
	"set config manually": build: #Build & {
		steps: [{
			output: #Image & {
				config: user: "foo"
			}
		}]
	}

	//    - import nginx base
	//    - copy app directory into /usr/share/nginx/html
	"build static web server": {
		source: dagger.#FS

		build: #Build & {
			steps: [
				nginx.#Build & {
					flavor: "alpine"
				},
				{
					// Custom step to watermark the image
					input:  _
					output: input & {
						config: user: "42"
					}
				},
				#Copy & {
					contents: source
					dest:     "/usr/share/nginx/html"
				},
			]
		}

		image: build.output
		// Assert:
		image: config: user: "42"
	}

	"Run multiple commands": {
		build: #Build & {
			steps: [
				#Pull & {
					source: "alpine"
				},
				#Run & {
					script: "echo A > /A.txt"
				},
				#Run & {
					script: "echo B > /B.txt"
				},
				#Run & {
					script: "echo C > /C.txt"
				},
			]
		}
		result: build.output
	}
}
