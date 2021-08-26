package op

import (
	"alpha.dagger.io/os"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
)

// Github PAT
TestPAT: dagger.#Input & {dagger.#Secret}

TestRepo: #up: [op.#FetchGit & {
	remote:    "https://github.com/dagger/dagger.git"
	ref:       "main"
	authToken: TestPAT
}]

TestContent: os.#Container & {
	always:  true
	command: "ls -l /input/repo | grep 'universe -> stdlib'"
	mount: "/input/repo": from: TestRepo
}
