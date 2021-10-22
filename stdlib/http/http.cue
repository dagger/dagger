package http

import (
	"encoding/json"
	// "strconv"

	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/os"
)

#Get:    #Do & {method: "GET"}
#Post:   #Do & {method: "POST"}
#Put:    #Do & {method: "PUT"}
#Delete: #Do & {method: "DELETE"}
#Do: {
	url:    string
	method: "GET" | "POST" | "PUT" | "DELETE" | "PATH" | "HEAD"

	request: {
		body: string | *""
		header: [string]: string | [...string]
		token: dagger.#Secret | *null
	}

	ctr: os.#Container & {
		image: alpine.#Image & {
			package: curl: true
			package: bash: "=5.1.0-r0"
			package: jq:   "~=1.6"
		}
		shell: path: "/bin/bash"
		always: true


		env: {
			METHOD:  method
			HEADERS: json.Marshal(request.header)
			BODY:    request.body
			URL:     url
		}
		if request.token != null {
			secret: "/token": request.token
		}
		command: #"""
			curlArgs=(
			    "$URL"
			    -L --fail --silent --show-error
			    --write-out "%{http_code}"
			    -X "$METHOD"
			    -o /response
			)

			[ -n "$BODY" ] && curlArgs+=("-d" "'$BODY'")

			headers="$(echo $HEADERS | jq -r 'to_entries | map(.key + ": " + (.value | tostring) + "\n") | add')"
			while read h; do
			    curlArgs+=("-H" "'$h'")
			done <<< "$headers"
			if [ -e /token ]; then
			    curlArgs+=("-H" "Authorization: bearer $(cat /token)")
			fi

			curl "${curlArgs[@]}" > /status
			"""#
	}

	response: {
		body: {
			os.#File & {
					from: ctr
					path: "/response"
				}
		}.contents  @dagger(output)
		statusCode: {
			os.#File & {
					from: ctr
					path: "/status"
				}
		}.contents @dagger(output)
	}
}
