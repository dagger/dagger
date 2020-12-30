package netlify

import (
	".../alpine"
)





auth: {
	#dag: {
		encrypted: true
		do: [
			{
				action: "fetch"
				type: "docker"
				source: "alpine"
			},
			{
				action: "push"
			}
		]
	}

	{
		username: string
		password: string
	} | {
		// FIXME: enrypted!
		token: string
	}
}

name: string
domain?: string
// FIXME: directory!
source: bl.#Dir

let base = alpine.#Image & {
	version: "foo"
	packages: ["rsync", "npm", "openssh"]
}

// Netlify site ID
id: {
	info1: string
	info2: string

	#dag: {
		// run code to fetch id from netlify API
		from: base
		do: [
			{
				action: "run"
				command: ["netlify-get-id", name, "-o", "/netlify-id.json"]
			}
		]
		export: json: "/netlify-id.json"
	}
}

url: string
