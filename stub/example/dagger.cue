// Netlify actions for Dagger

name:        "netlify"
description: "Deploy to netlify"

actions: {
	// Deploy a website to netlify
	deploy: {
		inputs: {
			// Contents of the site
			// contents: dagger.#FS

			// Name of the Netlify site
			// Example: "my-super-site"
			site: string

			// Netlify API token
			// token: dagger.#Secret

			// Name of the Netlify team (optional)
			// Example: "acme-inc"
			// Default: use the Netlify account's default team
			team: string | *""

			// Domain at which the site should be available (optional)
			// If not set, Netlify will allocate one under netlify.app.
			// Example: "www.mysupersite.tld"
			domain: string

			// Create the site if it doesn't exist
			create: *true | false
		}

		outputs: {
			// URL of the deployed site
			url: string

			// URL of the latest deployment
			deployUrl: string

			// URL for logs of the latest deployment
			logsUrl: string
		}
	}
}
