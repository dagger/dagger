// Netlify client operations
package netlify

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/os"
)

// Netlify account credentials
#Account: {
	// Use this Netlify account name
	// (also referred to as "team" in the Netlify docs)
	name: string | *"" @dagger(input)

	// Netlify authentication token
	token: dagger.#Secret @dagger(input)
}

// Netlify site
#Site: {
	// Netlify account this site is attached to
	account: #Account

	// Application context. The directory where the application contents reside.
	contents: dagger.#Artifact @dagger(input)

	// Deploy to this Netlify site
	name: string @dagger(input)

	// Create the Netlify site if it doesn't exist?
	create: bool | *true @dagger(input)

	// Host the site at this address
	customDomain?: string @dagger(input)

	ctr: os.#Container & {
		image: alpine.#Image & {
			package: {
				bash: "=~5.1"
				jq:   "=~1.6"
				curl: true
				yarn: "=~1.22"
			}
		}
		setup: [
			"yarn global add netlify-cli@4.1.18",
		]
		// set in netlify.sh.cue
		// FIXME: use embedding once cue supports it
		command: _
		env: {
			NETLIFY_SITE_NAME:  name
			NETLIFY_ACCOUNT:    account.name
			NETLIFY_DEPLOY_DIR: deployDir

			if (create) {
				NETLIFY_SITE_CREATE: "1"
			}

			if customDomain != _|_ {
				NETLIFY_DOMAIN: customDomain
			}
		}

		dir: "/src"
		mount: "/src": from: contents
		secret: "/run/secrets/token": account.token
	}

	// Website url
	url: {
		os.#File & {
				from: ctr
				path: "/netlify/url"
			}
	}.contents @dagger(output)

	// Unique Deploy URL
	deployUrl: {
		os.#File & {
				from: ctr
				path: "/netlify/deployUrl"
			}
	}.contents @dagger(output)

	// Logs URL for this deployment
	logsUrl: {
		os.#File & {
				from: ctr
				path: "/netlify/logsUrl"
			}
	}.contents @dagger(output)
}
