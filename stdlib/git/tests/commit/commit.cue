package git

import (
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/os"
	"alpha.dagger.io/random"
)

TestAuthToken: dagger.#Input & {dagger.#Secret}

TestRemote: dagger.#Input & {*"https://github.com/dagger/test.git" | string}

TestRepository: #Repository & {
	remote:     TestRemote
	ref:        "main"
	keepGitDir: true
	authToken:  TestAuthToken
}

TestData: {
	random.#String & {
		seed: "git-commit"
	}
}.out

// FIXME Currently throw a structural cycle error
//TestFile: os.#Dir & {
// from: os.#Container & {
//  image: alpine.#Image
//  command: #"""
//    mkdir -p /output
//    echo "$MESSAGE" >> /output/test.md
//   """#
//  env: MESSAGE: TestData
// }
// path: "/output"
//}

TestFile: #up: [
	op.#Load & {from: alpine.#Image},
	op.#Mkdir & {
		path: "/output"
	},
	op.#WriteFile & {
		content: TestData
		dest:    "/output/test.md"
	},
	op.#Subdir & {
		dir: "/output"
	},
]

TestCommit: #Commit & {
	repository: {
		remote:    TestRemote
		authToken: TestAuthToken
		source:    TestRepository
		branch:    "ci/test-commit"
	}
	content: TestFile
	message: "This is a commit from the CI to test the repository"
	name:    "Dagger CI"
	email:   "daggerci@dagger.io"
	force:   true
}

TestCheck: {
	_TestRepo: #Repository & {
		remote:     TestRemote
		ref:        "ci/test-commit"
		keepGitDir: true
		authToken:  TestAuthToken
	}

	_ctr: os.#Container & {
		image: #Image
		command: #"""
				# Check commit
				git rev-parse --verify HEAD | grep "$GIT_HASH"

				# Check file
				echo -n "$EXPECTED_MESSAGE" >> expect.md
				diff test.md expect.md
			"""#
		dir: "/input/repo"
		mount: "/input/repo": from: _TestRepo
		env: {
			EXPECTED_MESSAGE: TestData
			// Force dependency
			GIT_HASH: TestCommit.hash
		}
	}
}
