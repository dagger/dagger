package git

import (
	"strings"

	"alpha.dagger.io/git"
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/os"
)

repo: git.#Repository & {
	remote:     "https://github.com/blocklayerhq/acme-clothing.git"
	ref:        "master"
	keepGitDir: true
}

repoSubDir: git.#Repository & {
	remote:     "https://github.com/dagger/examples.git"
	ref:        "main"
	subdir:     "todoapp"
	keepGitDir: true
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

TestSubRepository: os.#Container & {
	image: alpine.#Image & {
		package: bash: "=5.1.0-r0"
		package: git:  true
	}
	mount: "/repo1": from: repoSubDir
	dir: "/repo1"
	command: """
		[ -d src ]
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
