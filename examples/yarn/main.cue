package main

import (
	"dagger.io/dagger"
	"dagger.io/yarn"
)

TestYarn: {
    run: #yarn.Script & {
        source: TestData
    }

    test: #dagger: compute: [
        dagger.#Load & { from: alpine.#Image & {
            package: bash: "=5.1.0-r0"
        }},
        dagger.#Exec & {
            mount: "/build": from: run
            args: [
                "/bin/bash",
                "--noprofile",
                "--norc",
                "-eo",
                "pipefail",
                "-c",
                """
                test "$(cat /build/test)" = "output"
                """
            ]
        }
    ]
}
