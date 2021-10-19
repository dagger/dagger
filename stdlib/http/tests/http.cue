package http

import (
	"encoding/json"

	"alpha.dagger.io/alpine"
	"alpha.dagger.io/os"
)

TestRequest: {
	req: #Get & {
		url: "https://api.github.com/"
		request: header: {
			Accept: "application/json"
			Test: ["A", "B"]
		}
	}

	testRaw: os.#Container & {
		image: alpine.#Image & {
			package: jq: "~=1.6"
		}
		env: STATUS: "\(req.response.statusCode)"
		files: "/content.json": {
			content: req.response.body
			mode:    0o500
		}
		command: #"""
			test "$STATUS" = 200
			test "$(cat /content.json | jq -r .current_user_url)" = "https://api.github.com/user"
			"""#
	}

	testJSON: os.#Container & {
		env: STATUS:  "\(req.response.statusCode)"
		env: CONTENT: json.Unmarshal(req.response.body).current_user_url
		command: """
			test "$STATUS" = 200
			test "$CONTENT" = "https://api.github.com/user"
			"""
	}
}
