package defn

import (
	"dagger.io/defns"
	"dagger.io/llb"
)

siteID: string & llb.#Input & {_, #help: "site id"}

acct: defns.#Account

site: defns.#Site & {
	name: siteID
}
