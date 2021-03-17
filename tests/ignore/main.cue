package main

import (
	"dagger.io/dagger"
	"dagger.io/llb"
)

dir: dagger.#Artifact

ignore: {
	string
	#compute: [
		llb.#FetchContainer & { ref: "debian:buster" },
		llb.#Exec & {
			args: ["bash", "-c", "ls -lh /src > /out.txt"]
			mount: "/src": { from: dir }
		},
		llb.#Export & { source: "/out.txt" },
	]
}

