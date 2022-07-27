package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	client: filesystem: "./build": write: contents: actions.build.gradle.export.directories."/build"
	actions: {
		build: {
			// core.#Source lets you access a file system tree (dagger.#FS)
			// using a path at "." or deeper (e.g. "./foo" or "./foo/bar") with
			// optional include/exclude of specific files/directories/globs
			checkoutCode: core.#Source & {
				path: "."
			}
			// Building an alpine image with gradle and bash installed
			base: alpine.#Build & {
				packages: {
					"gradle": _
					"bash":   _
				}
			}

			jdk: docker.#Pull & {
				source: "eclipse-temurin:11"
			}
			javaHome: docker.#Run & {
				input: jdk.output
				export: {
					directories: "/opt/java/openjdk": _
				}
			}
			copyJava: docker.#Copy & {
				input:    base.output
				contents: javaHome.export.directories."/opt/java/openjdk"
			}
			image: docker.#Copy & {
				input:    copyJava.output
				contents: checkoutCode.output
			}

			// Runs a bash script in the input container
			// in this case `gradle build` (or `gradlew`)
			gradle: bash.#Run & {
				input: image.output
				script: contents: """
					gradle build
					# gradlew
					"""
				export: directories: "/build": _
			}
		}
	}
}
