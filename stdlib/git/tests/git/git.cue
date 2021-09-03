package git

import (
	"strings"

	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/os"
)

repo: #Repository & {
	remote:     "https://github.com/blocklayerhq/acme-clothing.git"
	ref:        "master"
	keepGitDir: true
}

repoSubDir: #Repository & {
	remote:     "https://github.com/dagger/examples.git"
	ref:        "main"
	subdir:     "todoapp"
	keepGitDir: true
}

branch: #CurrentBranch & {
	repository: repo
}

tagsList: #Tags & {
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

// Test fetching a private repo
TestPAT: dagger.#Input & {dagger.#Secret}

privateRepo: #Repository & {
	remote:     "https://github.com/dagger/dagger.git"
	ref:        "main"
	keepGitDir: true
	authToken:  TestPAT
}

TestPrivateRepository: os.#Container & {
	image: alpine.#Image & {
		package: bash: "=5.1.0-r0"
		package: git:  true
	}
	mount: "/repo1": from: privateRepo
	dir: "/repo1"
	command: """
		[ -d .git ]
		"""
}
