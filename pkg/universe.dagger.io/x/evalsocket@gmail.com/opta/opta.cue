// Run a Opta program
package opta

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/docker"
	"universe.dagger.io/aws"
)

#DefaultLinuxVersion: "amazonlinux:2.0.20220121.0@sha256:f3a37f84f2644095e2c6f6fdf2bf4dbf68d5436c51afcfbfa747a5de391d5d62"
#DefaultCliVersion:   "2.4.12"

#Build: {
	docker.#Build & {
		steps: [
			docker.#Pull & {
				source: #DefaultLinuxVersion
			},
			docker.#Run & {
				command: {
					name: "yum"
					args: ["install", "unzip", "curl", "git", "yum-utils", "-y"]
				}
			},
			docker.#Run & {
				command: {
					name: "yum-config-manager"
					args: ["--add-repo", "https://rpm.releases.hashicorp.com/AmazonLinux/hashicorp.repo"]
				}
			},
			docker.#Run & {
				command: {
					name: "yum"
					args: ["install", "terraform", "-y"]
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
	_build: #Build

	// Source code of Opta program
	source: dagger.#FS

	// Opta action used for this Opta program
	action: "apply" | "destroy" | "force-unlock"

	// Opta environment used for this Opta program
	environment: string

	// Opta extra cli flags used for this Opta program
	extraArgs: string

	// Opta Config name used for this Opta program
	configFile: string | *"opta.yaml"

	// credentials provides long or short-term credentials
	credentials: aws.#Credentials

	// Run Opta apply
	container: docker.#Run & {
		input:  _build.output
		command: {
			name: "/scripts/opta.sh"
			args: []
		}
		env: {
			ACTION:  action
			ENV:  environment
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
		mounts: scripts: {
			dest:     "/scripts"
			contents: _scripts.output
		}
		mounts: opta: {
			dest:     "/src"
			contents: source
		}
	}

	_scripts: core.#Source & {
		path: "_scripts"
		include: ["*.sh"]
	}
}
