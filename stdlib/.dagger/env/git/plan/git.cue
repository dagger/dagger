package git

import (
	"strings"

	"dagger.io/git"
	"dagger.io/alpine"
	"dagger.io/os"
	"dagger.io/dagger/op"
)

repo: git.#Repository & {
	remote: "https://github.com/blocklayerhq/acme-clothing.git"
	ref:    "master"

	#up: [
		op.#FetchGit & {
			keepGitDir: true
		},
	]
}

branch: git.#CurrentBranch & {
	repository: repo
}

tagsList: git.#Tags & {
	repository: repo
}

TestRepository: os.#Container & {
	image: alpine.#Image & {
		package: bash: "=5.1.0-r0"
		package: git:  true
	}
	mount: "/repo1": from: repo
	dir: "/repo1"
	command: """
		[ -d .git ]
		"""
}

TestCurrentBranch: os.#Container & {
	image: alpine.#Image & {
		package: bash: "=5.1.0-r0"
		package: git:  true
	}
	env: BRANCH_NAME: branch.name
	command: """
		[ $BRANCH_NAME = "master" ]
		"""
}

TestCurrentTags: os.#Container & {
	image: alpine.#Image & {
		package: bash: "=5.1.0-r0"
		package: git:  true
	}
	env: TAGS: strings.Join([ for k, v in tagsList.tags {"\(k)=\(v)"}], "\n")
	command: """
		[ $TAGS = "0=master" ]
		"""
}
