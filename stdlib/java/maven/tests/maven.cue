package maven

import (
	"strings"

	"alpha.dagger.io/dagger"
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/os"
)

TestData: dagger.#Artifact

TestConfig: mavenOpts: strings.Join([
			"-client",
			"-XX:+TieredCompilation",
			"-XX:TieredStopAtLevel=1",
			"-Xverify:none",
			"-Dorg.slf4j.simpleLogger.log.org.apache.maven.cli.transfer.Slf4jMavenTransferListener=warn -Dorg.slf4j.simpleLogger.showDateTime=true",
			"-Dorg.slf4j.simpleLogger.showDateTime=true -Dorg.slf4j.simpleLogger.dateTimeFormat=HH:mm:ss",
], " ")

TestSpringBoot: {
	project: #Project & {
		source: TestData

		phases: ["install"]

		env: MAVEN_OPTS: TestConfig.mavenOpts

		args: ["--batch-mode"]
	}

	test: os.#Container & {
		image: alpine.#Image & {
			package: bash: "=5.1.0-r0"
		}
		copy: "/build": from: project.build
		command: """
			count=$(ls -1 /build/**/*.jar 2>/dev/null | wc -l)
			test "$count" != "0"
			"""
	}
}
