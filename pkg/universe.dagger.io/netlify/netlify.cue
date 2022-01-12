// Deploy to Netlify
// https://netlify.com
package netlify

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"

	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
)

// Deploy a site to Netlify
#Deploy: {
	// Contents of the site
	contents: dagger.#FS

	// Name of the Netlify site
	// Example: "my-super-site"
	site: string

	// Netlify API token
	token: dagger.#Secret

	// Name of the Netlify team (optional)
	// Example: "acme-inc"
	// Default: use the Netlify account's default team
	team: string | *""

	// Domain at which the site should be available (optional)
	// If not set, Netlify will allocate one under netlify.app.
	// Example: "www.mysupersite.tld"
	domain: string | *null

	// Create the site if it doesn't exist
	create: *true | false

	// Execute `netlify deploy` in a container
	command: bash.#Run & {
		// Container image. `netlify` must be available in the execution path
		*{
			_buildDefaultImage: docker.#Build & {
				input: alpine.#Build & {
					bash: version: "=~5.1"
					jq: version:   "=~1.6"
					curl: {}
					yarn: version: "=~1.22"
				}
				steps: [{
					run: script: "yarn global add netlify-cli@3.38.10"
				}]
			}

			// No nested tasks, boo hoo hoo
			image: _buildDefaultImage.output
			env: CUSTOM_IMAGE: "0"
		} | {
			env: CUSTOM_IMAGE: "1"
		}

		script: _deployScript // see deploy.sh
		always: true
		env: {
			NETLIFY_SITE_NAME: site
			if (create) {
				NETLIFY_SITE_CREATE: "1"
			}
			if domain != null {
				NETLIFY_DOMAIN: domain
			}
			NETLIFY_ACCOUNT: team
		}
		workdir: "/src"
		mounts: {
			"Site contents": {
				dest:       "/src"
				"contents": contents
			}
			"Netlify token": {
				dest:     "/run/secrets/token"
				contents: token
			}
		}
		output: files: {
			"/netlify/url":       _
			"/netlify/deployUrl": _
			"/netlify/logsUrl":   _
		}
	}

	// URL of the deployed site
	url: command.output.files."/netlify/url".contents

	// URL of the latest deployment
	deployUrl: command.output.files."/netlify/deployUrl".contents

	// URL for logs of the latest deployment
	logsUrl: command.output.files."/netlify/logsUrl".contents
}
