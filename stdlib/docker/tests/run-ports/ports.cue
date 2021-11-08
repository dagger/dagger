package docker

import (
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/os"
	"alpha.dagger.io/random"
)

suffix: random.#String & {
	seed: ""
}

run: #Run & {
	name:   "daggerci-test-ports-\(suffix.out)"
	ref:    "nginx"
	socket: dagger.#Stream & {unix: "/var/run/docker.sock"}
	ports: ["8080:80"]
	url: "http://localhost:8080/"
}

query: os.#Container & {
	image: alpine.#Image & {
		package: bash: "=~5.1"
		package: curl: true
	}
	command: #"""
		test "$(curl -L --fail --silent --show-error --write-out "%{http_code}" "$URL" -o /dev/null)" = "200"
		"""#
	env: URL: run.exposedURL
}
