package http

import (
	// "encoding/json"
	// "strconv"

	"alpha.dagger.io/alpine"
	"alpha.dagger.io/os"
)

TestRequest: {
	req: #Get & {
		url: "https://api.github.com/"
		request: header: {
			Accept: "application/json"
		}
	}

	testRaw: os.#Container & {
		image: alpine.#Image & {
			package: jq: "~=1.6"
			package: bash: true 
		}
		env: STATUS: "\(req.response.statusCode)"
		files: "/content.json": {
			content: req.response.body
			mode:    0o500
		}
		shell: {
			args: ["--noprofile", "--norc", "-eo", "pipefail", "-c"]
		}
		command: #Command
	}
}

#Command: #"""
			test "$(cat /content.json | jq -r .current_user_url)" = 'https://api.github.com/user'
			test "$STATUS" = "200"
			"""#
