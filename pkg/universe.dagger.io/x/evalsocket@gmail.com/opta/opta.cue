// Run a Opta program
package opta

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/docker"
	"universe.dagger.io/bash"
)

#DefaultLinuxVersion: "amazonlinux:2.0.20220121.0@sha256:f3a37f84f2644095e2c6f6fdf2bf4dbf68d5436c51afcfbfa747a5de391d5d62"
#DefaultCliVersion:   "2.4.12"

// Build provides a docker.#Image with the aws cli pre-installed to Amazon Linux 2.
// Can be customized with packages, and can be used with docker.#Run for executing custom scripts.
// Used by default with aws.#Run
#Build: {
	docker.#Build & {
		steps: [
			docker.#Pull & {
				source: #DefaultLinuxVersion
			},
			// cache yum install separately
			docker.#Run & {
				command: {
					name: "yum"
					args: ["install", "unzip", "curl", "git", "-y"]
				}
			},
			docker.#Run & {
				command: {
					name: "/scripts/install.sh"
					args: [version]
				}
				mounts: scripts: {
					dest:     "/scripts"
					contents: _scripts.output
				}
			},
		]
	}

	_scripts: core.#Source & {
		path: "_scripts"
		include: ["*.sh"]
	}

	// The version of the AWS CLI to install
	version: string | *#DefaultCliVersion
}

// Run a `opta Apply`
#Action: {
	// Source code of Opta program
	source: dagger.#FS

	// Opta action used for this Opta program
	action: "apply" | "destroy" | "force-unlock"

	// Opta env used for this Opta program
	env: string

	// Opta extra cli flags used for this Opta program
	extraArgs: string

	// Opta Config name used for this Opta program
	configFile: string | *"opta.yaml"

	// credentials provides long or short-term credentials
	credentials: aws.#Credentials

	// Run Opta apply
	container: bash.#Run & {
		input: docker.#Image | *_build.output
		script: {
			_load: core.#Source & {
				path: "."
				include: ["*.sh"]
			}
			directory: _load.output
			filename:  "opta.sh"
		}
		env: {
			ACTION:  action
			ENV:  env
			CONFIG_FILE:  configFile
			EXTRA_ARGS: extraArgs

			// pass credentials as env vars
			if credentials.accessKeyId != _|_ {
				AWS_ACCESS_KEY_ID: credentials.accessKeyId
			}

			if credentials.secretAccessKey != _|_ {
				AWS_SECRET_ACCESS_KEY: credentials.secretAccessKey
			}

			if credentials.sessionToken != _|_ {
				AWS_SESSION_TOKEN: credentials.sessionToken
			}
		}
		workdir: "/src"
		mounts: {
			src: {
				dest:     "/src"
				contents: source
			}
			src: {
				dest:     "/src"
				contents: _scripts.output
			}
		}
	}

	_scripts: core.#Source & {
		path: "_scripts"
		include: ["*.sh"]
	}
}
