package docker

import (
	"dagger.io/dagger"
	"universe.dagger.io/nginx"
)

build0: #Build & {
	steps: [
		{
			output: #Image & {
				config: user: "foo"
			}
		},
	]
}

// Inventory of real-world build use cases
//
// 1. Build netlify image
//    - import alpine base
//    - execute 'yarn add netlify'

// 2. Build todoapp dev image
//    - import nginx base
//    - copy app directory into /usr/share/nginx/html

build2: {
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
	img: build.output

	// Assert:
	img: config: user: "42"
}

// 3. Build alpine base image
//    - pull from docker hub
//    - execute 'apk add' once per package

// 4. Build app from dockerfile

// 5. execute several commands in a row

build3: {
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
