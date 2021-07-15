package os

import (
	"alpha.dagger.io/alpine"
)

// Create an artifact
TestArtifact: #Dir & {
	from: #Container & {
		always:  true
		command: "mkdir -p /output/ && echo 'hello world' > /output/out.txt"
	}
	path: "/output"
}

TestZip: {
	zip: #Zip & {
		source: TestArtifact
	}

	test: #Container & {
		image: alpine.#Image & {
			package: file: true
		}
		always:  true
		command: "file /output/\(zip.name) | grep 'Zip'"
		mount: "/output": from: zip
	}
}
