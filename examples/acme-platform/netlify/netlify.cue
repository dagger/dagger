// Custom netlify package
// ACME platform team <platform@acme.infralabs.io>
//
// TODO: upstream to dagger standard library.
package netlify

import (
	"dagger.cloud/dagger"
)

// Netlify API token
token: {
	#dag: {
		encrypt: cipher: "..."
	}

	string
}


// Netlify site name
name?: string

// Source directory to deploy
source: dagger.#Dir


let apply={
	#dag: {
		from: alpine.#Base
		do: [
			["run", "npm", "install", "netlify-cli", "-g"],
			[
				"copy",
				[
					"fetch", "git", "https://github.com/shykes/tests", "netlify-scripts",
				], "/", "/src",
			]
			// 2. fetch custom netlify scripts & iunstall
			// 3. get ID from name; create if doesn't exist
			// 4. deploy (via builder)
		]
		command: {
			debug: {
				from: base
				do: ["run", "sh", "-c", """
					env && find /netlify
					"""]
			}
		}
	}
}
apply

deployedDir: {
	#dag: {
		from: apply
		export: dir: "/netlify/content"
	}
}

// Netlify site ID
ID: {
	string

	#dag: {
		from: apply
		export: string: "/netlify/site-id"
	}
}

url: {
	string

	#dag: {
		from: apply
		export: string: "/netlify/url"
	}
}


		// Example of short-form cuellb pipeline
		//   1. single-op pipeline can omit the array
		//   2. action encoded in first key, instead of `action: ` field
		//   3. op may implement short-form,
		//			in this case: `run: [...string]` instead of `run: { command: [...string] }`
		do: run: ["ntlfy-get-site-id", name, "-o", "/netlify/site-id"]
		// Declarative export from container, instead of awkward `readFile` pseudo-op
		export: string: "/netlify/site-id"
	}
}


// Configuration presets
preset: {
	*"html" | "react" | "custom"

	#dag: {
		settings: {
			markup: select: {
				"Static HTML site (no build)": "html"
				"ReactJS app built with npm": "react"
				"Custom builder": "custom"
			}
		}
	}
}

// Custom builder
// Default: no build, deploy as-is.
builder: {
	in: dagger.#Dir & source
	out: dagger.#Dir

	if preset == "html" {
		// Pass-through builder that does nothing
		out: in
	}
	if preset == "react" {
		let app = reactjs.#App & {
			source: in
		}
		out: app.build
	}

	...
}


scripts: {
	dagger.#Directory | *latestScripts

	let latestScripts = {
		#dag: {
				do: {
					action: "fetch"
					type: "git"
					source: "https://github.com/shykes/tests"
					ref: "netlify-scripts"
				}
			}
			export: dir: "/"
		}
	}

	// This is configurable for dev mode, but hide it from end users.
	#dag: settings: hidden: true
}

// Version of the netlify CLI to use
cliVersion: string | *latestCLIVersion

let latestCLIVersion = {
	string

	#dag: {
		from: base 
		do: run: ["sh", "-c", "npm show netlify-cli dist-tags.latest > /latest-cli-version"]
		export: string: "/latest-cli-version"
	}
}

// Custom container to run netlify commands + wrappers
let base=alpine.#Base & {
	package: {
		npm: true
		curl: true
	}
}

let runner = {
	#dag: {
		from: base
		do: [
			{
				run: "npm", "install
				action: "run"
				command: ["npm", "install", "-g", "netlify-cli@" + cliVersion]
			},
			{
			// YOU ARE HERE
			// incorporate "netify scripts from personal github" pattern from other POC	
			}
	}
}

url: {
	string

	#dag: {
		from: runner
		do: run: {
			command: #"""
				netlify deploy
			   	 --dir="$(pwd)" \
			   	 --auth="$(cat /netlify/token)" \
			   	 --site="${NETLIFY_SITE_ID}" \
			   	 --message="Blocklayer 'netlify deploy'" \
			   	 --prod \
				| tee /tmp/stdout
				curl \
					-i -X POST \
					-H "Authorization: Bearer $(cat /netlify/token)" \
					"https://api.netlify.com/api/v1/sites/${NETLIFY_SITE_ID}/ssl"
				"""#
			mount: {
				"/netlify/token": token
				"/netlify/source": builder.out
			}
			dir: "/netlify/source"
			env: {
				NETLIFY_SITE_ID: ID
			}
		}
	}
}
