package git

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/os"
)

// Commit & push to git repository
#Commit: {
	// Git repository
	repository: {
		// Repository source code
		source: dagger.#Artifact

		// Repository remote URL
		remote: dagger.#Input & {string}

		// Authentication token (PAT or password)
		authToken: dagger.#Input & {*null | dagger.#Secret}

		// Git branch
		branch: dagger.#Input & {string}
	}

	// Username
	name: dagger.#Input & {string}

	// Email
	email: dagger.#Input & {string}

	// Commit message
	message: dagger.#Input & {string}

	// Content to commit
	content: dagger.#Artifact

	// Force push options
	force: dagger.#Input & {*false | bool}

	_ctr: os.#Container & {
		image: #Image
		command: #"""
				# Move changes into repository
				mv /input/content/* .

				# Commit changes
				git add .
				git -c user.name="$USER_NAME" -c user.email="$USER_EMAIL" commit -m "$COMMIT_MESSAGE"

				# Push
				git push "$OPT_FORCE" -u "$GIT_REMOTE" HEAD:refs/heads/"$GIT_BRANCH"

				# Retrieve commit hash
				git rev-parse --verify HEAD | tr -d '\n' > /commit.txt
			"""#
		dir: "/input/repo"
		mount: {
			"/input/repo": from:    repository.source
			"/input/content": from: content
		}
		env: {
			USER_NAME:      name
			USER_EMAIL:     email
			COMMIT_MESSAGE: message
			GIT_BRANCH:     repository.branch
			GIT_REMOTE:     repository.remote
			if force {
				OPT_FORCE: "-f"
			}

		}
		if repository.authToken != null {
			env: GIT_ASKPASS: "/get_authToken"
			files: "/get_authToken": {
				content: "cat /secrets/authToken"
				mode:    0o500
			}
			secret: "/secrets/authToken": repository.authToken
		}
	}

	// Commit hash
	hash: {
		os.#File & {
			from: _ctr
			path: "/commit.txt"
		}
	}.contents & dagger.#Output
}
