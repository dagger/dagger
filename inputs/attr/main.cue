package attr

import (
	"dagger.io/attrs"
)

siteID: string @input(help="site ID")

acct: attrs.#Account

site: attrs.#Site & {
	name: siteID
}
