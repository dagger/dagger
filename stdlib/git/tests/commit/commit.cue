package git

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/os"
	"alpha.dagger.io/random"
)

TestPAT: dagger.#Input & {dagger.#Secret}

TestRemote: dagger.#Input & {*"https://github.com/dagger/test.git" | string}

TestRepository: #Repository & {
	remote:     TestRemote
	ref:        "main"
	keepGitDir: true
	authToken:  TestPAT
}

TestData: {
	random.#String & {
		seed: "git-commit"
	}
}.out

TestFile: os.#Dir & {
	from: os.#Container & {
		command: #"""
				mkdir -p /output
				echo "$MESSAGE" >> /output/test.md
			"""#
		env: MESSAGE: TestData
	}
	path: "/output"
}

TestCommit: #Commit & {
	repository: {
		remote: TestRemote
		PAT:    TestPAT
		source: TestRepository
		branch: "ci/test-commit"
	}
	content: TestFile
	message: "This is a commit from the CI to test the repository"
	name:    "Dagger CI"
	email:   "daggerci@dagger.io"
	force:   true
}

TestCheck: {
	_TestRepo: #Repository & {
		remote:     TestCommit.repository.remote
		ref:        TestCommit.repository.branch
		keepGitDir: true
		authToken:  TestCommit.repository.PAT
	}

	_TestHash: TestCommit.hash

	os.#Container & {
		image:   #Image
		command: #"""
			# Check commit
			# FIXME Interpolate because there is an empty disjuction error
			# when given to env
			git rev-parse --verify HEAD | grep \#(TestCommit.hash)

			# Check file
			echo "$MESSAGE" >> expect.md
			diff test.md expect.md
		"""#
		dir:     "/input/repo"
		mount: "/input/repo": from: _TestRepo
		env: {
			MESSAGE: TestData
			// Force dependency
			// GIT_HASH: TestCommit.hash
		}
	}
}
