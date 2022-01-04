package http

import (
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
	"alpha.dagger.io/os"
	"alpha.dagger.io/random"
)

TestDockersocket: dagger.#Stream & dagger.#Input

TestSuffix: random.#String & {
	seed: ""
}

TestRun: docker.#Run & {
	name:   "daggerci-test-wait-\(TestSuffix.out)"
	ref:    "nginx"
	socket: TestDockersocket
	ports: ["8088:80"]
}

// Waits for TestRun to finish initializing
Testhealth: #Wait & {
	url: "http://localhost:8088/"
}

TestWait: query: os.#Container & {
	image: alpine.#Image & {
		package: bash: true
		package: curl: true
	}
	command: #"""
		test "$(curl -L --fail --silent --show-error --write-out "%{http_code}" "$URL" -o /dev/null)" = "200"
		"""#
	env: URL: Testhealth.url
}

TestRequest: {
	req: #Get & {
		url: Testhealth.url
	}

	testRaw: os.#Container & {
		image: alpine.#Image & {
			package: jq:   true
			package: bash: true
		}
		env: STATUS: "\(req.response.statusCode)"
		files: "/content.json": {
			content: req.response.body
			mode:    0o500
		}
		shell: args: ["--noprofile", "--norc", "-eo", "pipefail", "-c"]
		command: #Command
	}
	#Command: #"""
		cat /content.json | grep -q nginx >/dev/null
		test "$STATUS" = "200"
		"""#
}
