package netlify

import (
	"dagger.io/dagger"
	"dagger.io/alpine"
	"dagger.io/os"
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
	url: {
		os.#File & {
			from: ctr
			path: "/netlify/url"
		}
	}.read.data

	// Unique Deploy URL
	deployUrl: {
		os.#File & {
			from: ctr
			path: "/netlify/deployUrl"
		}
	}.read.data

	// Logs URL for this deployment
	logsUrl: {
		os.#File & {
			from: ctr
			path: "/netlify/logsUrl"
		}
	}.read.data

	ctr: os.#Container & {
		image: alpine.#Image & {
			package: {
				bash: "=~5.1"
				jq:   "=~1.6"
				curl: "=~7.76"
				yarn: "=~1.22"
			}
		}
		setup: [
			"yarn global add netlify-cli@2.47.0",
		]
		// set in netlify.sh.cue
		// FIXME: use embedding once cue supports it
		command: _
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
	}
}
