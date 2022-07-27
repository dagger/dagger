package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	//Write the output of the gradle build to the client dev machine
	client: {
		filesystem: {
			"./build": write: contents: actions.build.gradle.export.directories."/build"
		}
	}

	actions: {
		build: {
			// core.#Source lets you access a file system tree (dagger.#FS)
			// using a path at "." or deeper (e.g. "./foo" or "./foo/bar") with
			// optional include/exclude of specific files/directories/globs
			checkoutCode: core.#Source & {
				path: "."
			}
			// Build an alpine image with gradle and bash installed
			base: alpine.#Build & {
				packages: {
					"gradle": _
					"bash":   _
				}
			}
			// Pull image with OpenJDK from Docker Hub
			jdk: docker.#Pull & {
				source: "eclipse-temurin:11"
			}
			// User docker.#Run to export openjdk dir from jdk container
			javaHome: docker.#Run & {
				input: jdk.output
				export: {
					directories: "/opt/java/openjdk": _
				}
			}
			// Copy the openjdk contents to the alpine gradle image
			copyJava: docker.#Copy & {
				input:    base.output
				contents: javaHome.export.directories."/opt/java/openjdk"
			}
			// Finally copy the source code into the image
			image: docker.#Copy & {
				input:    copyJava.output
				contents: checkoutCode.output
			}
			// Runs a bash script in the input container
			// in this case `gradle wrapper`, `gradlew...`
			// for more control over gradle version for build, etc.
			// Simple `gradle build` can be used instead, just invert commments #
			// export the /build directory to write to client machine
			gradle: bash.#Run & {
				input: image.output
				script: contents: """
					#gradle build
					gradle wrapper --gradle-version 7.5
					./gradlew build
					./gradlew run
					"""
				export: directories: "/build": _
			}
		}
	}
}
