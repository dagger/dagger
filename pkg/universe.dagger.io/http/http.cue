package http

import (
  "dagger.io/dagger"
  "dagger.io/dagger/engine"

	"encoding/json"

	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
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


_build: docker.#Build & {
		steps: [
			alpine.#Build & {
				packages: {
					bash: {}
					curl: {}
					jq: {}
				}
			},
		]
	}

  command: docker.#Run & {
    image: _build.output
    
    always: true
    env: {
      METHOD:  method
      HEADERS: json.Marshal(request.header)
      BODY:    request.body
      URL:     url
    }
    if request.token != null {
      mounts: {
        "Request token": {
          dest:     "/token"
          contents: request.token
        }
      }
    }
    script: #"""
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

  statusCode: engine.#ReadFile & {
    input: command.output.rootfs
    path:  "/status"
  }

  response: engine.#ReadFile & {
    input: command.output.rootfs
    path:  "/response"
  }
}