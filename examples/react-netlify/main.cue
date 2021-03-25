package main

import (
	"dagger.io/netlify"
	"dagger.io/yarn"
	"dagger.io/git"
)

repository: git.#Repository & {
	remote: "https://github.com/kabirbaidhya/react-todo-app.git"
	ref:    "624041b17bd62292143f99bce474a0e3c2d2dd61"
}

todoApp: netlify.#Site & {
	account: {
		// Create an API token in your Netlify account settings
		// https://app.netlify.com/user/applications/personal
		//
		// Fill using --input-string todoApp.account.token=XXX
		token: string
	}

	name: "dagger-example-react-netlify"

	contents: yarn.#Script & {
		source: repository
		run:    "build"
		env: "xx" :"bar"
	}
}
