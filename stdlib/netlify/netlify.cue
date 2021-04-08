package netlify

import (
	"dagger.io/dagger"
	"dagger.io/alpine"
	"dagger.io/docker"
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
	deployUrl: string

	// Logs URL for this deployment
	logsUrl: string

	// Deployment container
	#deploy: docker.#Container & {
		image: alpine.#Image & {
			package: {
				bash: "=~5.1"
				jq:   "=~1.6"
				curl: "=~7.74"
				yarn: "=~1.22"
			}
		}
		setup: [
			"yarn global add netlify-cli@2.47.0",
		]
		shell: {
			path: "/bin/bash"
			args: [
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"-c",
			]
		}
		dir: "/src"
		volume: "contents": {
			dest: "/src"
			from: contents
		}
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
		export: {
			source: "/output.json"
			format: "json"
		}
	}

	// FIXME: this is a hack to use docker.#Container while exporting 
	// values.
	#up: #deploy.#up
}
