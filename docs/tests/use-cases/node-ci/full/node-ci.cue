package docs

import (
	"dagger.io/dagger"
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	client: {
		filesystem: "./": read: {
			contents: dagger.#FS
			exclude: [
				"README.md",
				"eden.cue",
			]
		}
		env: {
			SONAR_LOGIN:     dagger.#Secret
			GITHUB_HEAD_REF: GITHUB_HEAD_REF
		}
	}
	actions: {
		deps: {
			node:
				docker.#Build & {
					steps: [
						docker.#Pull & {
							source: "index.docker.io/node"
						},
						docker.#Copy & {
							contents: client.filesystem."./".read.contents
							dest:     "./src"
						},
					]
				}
			sonarscanner:
				docker.#Build & {
					steps: [
						docker.#Pull & {
							source: "index.docker.io/sonarsource/sonar-scanner-cli"
						},
						docker.#Copy & {
							contents: client.filesystem."./".read.contents
							dest:     "/usr/src"
						},
					]
				}
		}

		build: {
			bash.#Run & {
				workdir: "./src"
				input:   deps.node.output
				script: contents: #"""
					npm ci
					"""#
			}
		}

		staticAnalysis: {
			lint:
				bash.#Run & {
					workdir: "./src"
					input:   build.output
					script: contents: #"""
						npx eslint --color .
						"""#
				}
			sonarscanner:
				docker.#Run & {
					env: {
						GITHUB_BRANCH_NAME: client.env.GITHUB_HEAD_REF
						SONAR_LOGIN:        client.env.SONAR_LOGIN
						SONAR_HOST_URL:     "https://sonarcloud.io"
					}
					workdir: "/usr/src"
					input:   deps.sonarscanner.output
				}
		}

		test: {
			integrationTest: {
				workdir: "./src"
				docker.#Run & {
					input: build.output
					command: {
						name: "/bin/bash"
						args: ["-c", "npm run test:ci"]
					}
				}
			}
			unitTest: {
				workdir: "./src"
				docker.#Run & {
					input: build.output
					command: {
						name: "/bin/bash"
						args: ["-c", "npm run test:unit"]
					}
				}
			}
		}

		SCA: dependencyScanning: {
			docker.#Run & {
				workdir: "./src"
				input:   build.output
				command: {
					name: "/bin/bash"
					args: ["-c", "npx audit-ci --high"]
				}
			}
		}
	}
}
