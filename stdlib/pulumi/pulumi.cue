// Pulumi operations
package pulumi

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
)

// Pulumi configuration
#Configuration: {

	// Pulumi version
	version: string | *"latest" @dagger(input)

	// Runtime
	runtime: "nodejs" | "python" | "dotnet" | "go" @dagger(input)

	// Source configuration
	source: dagger.#Artifact @dagger(input)

	// Stack
	stack: string @dagger(input)

  	// Token
  	token: dagger.#Secret @dagger(input)

	// Environment variables
	env: {
		[string]: string @dagger(input)
	}

	state: #up: [
		op.#FetchContainer & {
			ref: "pulumi/pulumi:\(version)"
		},

		op.#Copy & {
			from: source
			dest: "/src"
		},

		op.#Exec & {
			args: ["pulumi", "login", "https://api.pulumi.com"],
			mount: "/access_token": secret: token
			env: {
				"PULUMI_ACCESS_TOKEN": "$(cat /access_token)"
			}
		}

		if runtime == "nodejs" {
			op.#Exec & {
				args: ["npm", "install"],
				dir:   "/src"
			}
		}

		op.#Exec & {
			args: ["pulumi", "up", "--stack", stack, "--skip-preview", "--yes"]
			always: true
			dir:    "/src"
			"env":  env
		},
	]

	output: {
		#up: [
			op.#Load & {from: state},
			op.#Exec & {
				args: ["sh", "-c", "pulumi stack output -json > /output.json"]
				dir:   "/src"
				"env": env
			},
			op.#Export & {
				source: "/output.json"
				format: "json"
			},
		]
		...
	} @dagger(output)
}
