package netlify

import (
	"dagger.io/dagger"
	"dagger.io/alpine"
)

// A Netlify account
#Account: {
	// Use this Netlify account name
	// (also referred to as "team" in the Netlify docs)
	name: string | *""

	// Netlify authentication token
	token: string
}

// A Netlify site
#Site: {
	// Netlify account this site is attached to
	account: #Account

	// Contents of the application to deploy
	contents: dagger.#Dir

	// Deploy to this Netlify site
	name: string

	// Host the site at this address
	customDomain: string

	// Create the Netlify site if it doesn't exist?
	create: bool | *true

	// Deployment url
	url: {
		string

		#dagger: compute: [
			dagger.#Load & {
				from: alpine.#Image & {
					package: bash: "=5.1.0-r0"
					package: jq:   "=1.6-r1"
					package: curl: "=7.74.0-r0"
					package: yarn: "=1.22.10-r0"
				}
			},
			dagger.#Exec & {
				args: ["yarn", "global", "add", "netlify-cli@2.47.0"]
			},
			dagger.#Exec & {
				args: [
					"/bin/bash",
					"--noprofile",
					"--norc",
					"-eo",
					"pipefail",
					"-c",
					code,
				]
				env: {
					NETLIFY_SITE_NAME: name
					if (create) {
						NETLIFY_SITE_CREATE: "1"
					}
					if customDomain != _|_ {
						NETLIFY_DOMAIN: customDomain
					}
					NETLIFY_ACCOUNT:    account.name
					NETLIFY_AUTH_TOKEN: account.token
				}
				dir: "/src"
				mount: "/src": from: contents
			},
			dagger.#Export & {
				source: "/url"
				format: "string"
			},
		]
	}
}

// FIXME: this should be outside
let code = #"""
	create_site() {
	    url="https://api.netlify.com/api/v1/${NETLIFY_ACCOUNT:-}/sites"

	    response=$(curl -s -S -f -H "Authorization: Bearer $NETLIFY_AUTH_TOKEN" \
	                -X POST -H "Content-Type: application/json" \
	                $url \
	                -d "{\"name\": \"${NETLIFY_SITE_NAME}\", \"custom_domain\": \"${NETLIFY_DOMAIN}\"}"
	            )
	    if [ $? -ne 0 ]; then
	        exit 1
	    fi

	    echo $response | jq -r '.site_id'
	}

	site_id=$(curl -s -S -f -H "Authorization: Bearer $NETLIFY_AUTH_TOKEN" \
	            https://api.netlify.com/api/v1/sites\?filter\=all | \
	            jq -r ".[] | select(.name==\"$NETLIFY_SITE_NAME\") | .id" \
	        )
	if [ -z "$site_id" ] ; then
	    if [ "${NETLIFY_SITE_CREATE:-}" != 1 ]; then
	        echo "Site $NETLIFY_SITE_NAME does not exist"
	        exit 1
	    fi
	    site_id=$(create_site)
	    if [ -z "$site_id" ]; then
	        echo "create site failed"
	        exit 1
	    fi
	fi
	netlify deploy \
	    --dir="$(pwd)" \
	    --site="$site_id" \
	    --prod \
	| tee /tmp/stdout

	</tmp/stdout sed -n -e 's/^Website URL:.*\(https:\/\/.*\)$/\1/p' | tr -d '\n' > /url
	"""#
