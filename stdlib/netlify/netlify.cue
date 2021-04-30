package netlify

import (
	"dagger.io/dagger"
	"dagger.io/alpine"
	"dagger.io/dagger/op"
)

// A Netlify account
#Account: {
	// Use this Netlify account name
	// (also referred to as "team" in the Netlify docs)
	name: string | *""

	// Netlify authentication token
	token: dagger.#Secret
}

// A Netlify site
#Site: {
	// Netlify account this site is attached to
	account: #Account

	// Contents of the application to deploy
	contents: dagger.#Artifact

	// Deploy to this Netlify site
	name: string

	// Host the site at this address
	customDomain?: string

	// Create the Netlify site if it doesn't exist?
	create: bool | *true

	// Website url
	url: string

	// Unique Deploy URL
	deployUrl: string @dagger(computed)

	// Logs URL for this deployment
	logsUrl: string @dagger(computed)

	#up: [
		op.#Load & {
			from: alpine.#Image & {
				package: bash: "=~5.1"
				package: jq:   "=~1.6"
				package: curl: "=~7.76"
				package: yarn: "=~1.22"
			}
		},
		op.#Exec & {
			args: ["yarn", "global", "add", "netlify-cli@2.47.0"]
		},
		op.#Exec & {
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"-c",
				#code,
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
		op.#Export & {
			source: "/output.json"
			format: "json"
		},
	]
}
