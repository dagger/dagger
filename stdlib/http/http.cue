package http

import (
	"encoding/json"
	"strconv"

	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
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

	statusCode: {
		os.#File & {
				from: ctr
				path: "/status"
			}
	}.contents @dagger(output)

	body: {
		os.#File & {
				from: ctr
				path: "/response"
			}
	}.contents @dagger(output)

	// Force os.#File exec before Atoi
	response: {
		"body":       body
		"statusCode": strconv.Atoi(statusCode)
	}
}

// URL listener
// Creates a dependency on URL
#Wait: {
	// URL to listen
	url: string

	// Waiting time between checks (sec.)
	interval: int | *30

	// Max amount of retries
	retries: int | *3

	// Max initialization time (sec.)
	startPeriod: int | *0

	// Time until timeout (sec.)
	timeout: int | *30

	// Env variables
	env: [string]: string

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				package: bash: "=~5.1"
				package: curl: true
			}
		},
		op.#Exec & {
			args: ["/bin/bash", "-c",
				#"""
					# (f: str -> int)
					nb_retries=$(($NB_RETRIES+0))
					starting_period=$(($START_PERIOD+0))

					status="0"
					SECONDS=0
					# START_PERIOD implementation
					while [ $SECONDS -lt $starting_period ]; do
						status="$(curl --connect-timeout 1 -s -o /dev/null -w ''%{http_code}'' $HEALTH_URL)"
						if [ "$status" == "200" ]; then
							exit 0;
						fi
						sleep 1;
					done

					# TIMEOUT, INTERVAL, INTERVAL implementation
					for ((i=0;i<NB_RETRIES;i++)); do
						status="$(curl --connect-timeout $TIMEOUT -s -o /dev/null -w ''%{http_code}'' $HEALTH_URL)"
						if [ "$status" == "200" ]; then
							exit 0;
						fi
						sleep "$INTERVAL";
					done

					exit 1;
					"""#,
			]
			always: true
			"env": {
				HEALTH_URL:   url
				INTERVAL:     "\(interval)"
				NB_RETRIES:   "\(retries)"
				START_PERIOD: "\(startPeriod)"
				TIMEOUT:      "\(timeout)"
				for k, v in env {
					"\(k)": v
				}
			}
		},
	]
}
