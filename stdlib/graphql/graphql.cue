package graphql

import (
	"encoding/json"

	"alpha.dagger.io/dagger"
	"alpha.dagger.io/http"
)

#Query: {
	// Contents of the graphql query
	query: string
	// graphql variables
	variable: [key=string]: _
	// graphql url
	url: string
	//API Token
	token: dagger.#Input & dagger.#Secret | *null

	post: http.#Post & {
		"url": url
		request: {
			body: json.Marshal({
				"query":   query
				variables: json.Marshal(variable)
			})
			header: "Content-Type": "application/json"
			token: token
		}
	}

	payload: {
		data: {...}
		errors?: {...}
	}
	payload: json.Unmarshal(post.response.body)
	data:    payload.data   @dagger(output)
	errors?: payload.errors @dagger(output)
}
