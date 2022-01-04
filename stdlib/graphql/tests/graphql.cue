package graphql

import (
	"encoding/json"

	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
	"alpha.dagger.io/os"
	"alpha.dagger.io/http"
)

TestDockersocket: dagger.#Stream & dagger.#Input

TestQuery: {

	run: docker.#Run & {
		name:   "graphql-faker"
		ref:    "apisguru/graphql-faker"
		socket: TestDockersocket
		ports: ["8080:9002"]
	}

	// Waits for TestRun to finish initializing
	Testhealth: http.#Wait & {
		url: "http://localhost:8080/graphql?query={%7BallCompanies%20%7B%0A%20%20%20%20id%0A%20%20%7D%0A%7D}"
	}

	queryWithoutToken: #Query & {
		url: Testhealth.url
		query: #"""
			{
				company(id: "NjExNjAwMjE5Nw==") {
					id
				}
			}
			"""#
	}

	testRaw: os.#Container & {
		image: alpine.#Image & {
			package: jq: true
		}
		env: STATUS: "\(queryWithoutToken.post.response.statusCode)"
		shell: args: ["--noprofile", "--norc", "-eo", "pipefail", "-c"]
		files: "/content.json": {
			content: json.Marshal(queryWithoutToken.data)
			mode:    0o500
		}
		command: #"""
			test "$STATUS" = "200"
			test "$(cat /content.json | jq '.allCompanies | length')" -gt 1
			"""#
	}
}
