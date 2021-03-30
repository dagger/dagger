package defns

import (
	"strings"

	"dagger.io/llb"
)

#Account: {
	id: string & llb.#Input & {_,  #help: "account id" }
	apikey: string & llb.#Input & {_,  #help: "apikey" }
}

#Site: {
	acct: #Account
	name: string & llb.#Input & {_,  #help: "name of site" }
	url: string & strings.MinRunes(2)
	deployURL: string & llb.#Output & {_,  #help: "deployment URL" }
}

