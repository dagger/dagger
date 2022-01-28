// Deploy to Netlify
// https://netlify.com
package netlify

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
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

	// Run the deployment script in a container
	_run: bash.#Run & {
		// Load embedded shell scripts
		source:       _loadScripts.output
		_loadScripts: dagger.#Source & {
			path: "."
			include: ["*.sh"]
		}

		// Customize the Docker container for netlify
		container: {
			image:  *#defaultImage | _
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
					dest:       "/netlify/contents"
					"contents": contents
				}
				"Netlify token": {
					dest:     "/run/secrets/token"
					contents: token
				}
			}
			export: files: {
				"/netlify/url":       _
				"/netlify/deployUrl": _
				"/netlify/logsUrl":   _
			}
		}
	}

	// URL of the deployed site
	url: _run.container.export.files."/netlify/url".contents

	// URL of the latest deployment
	deployUrl: _run.container.export.files."/netlify/deployUrl".contents

	// URL for logs of the latest deployment
	logsUrl: _run.container.export.files."/netlify/logsUrl".contents
}

// A ready-to-use Docker image with the netlify CLI & bash installed
#defaultImage: {
	_build: docker.#Build & {
		steps: [
			alpine.#Build & {
				packages: {
					bash: {}
					curl: {}
					jq: {}
					yarn: {}
				}
			},
			docker.#Run & {
				command: {
					name: "yarn"
					args: ["global", "add", "netlify-cli@8.6.21"]
				}
			},
		]
	}
	_build.output
}
