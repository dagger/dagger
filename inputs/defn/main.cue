package defn

import (
	"strings"
)

#Input: {
	_input: true
	_
	...
}

#Site: {
	name: string & #Input & {_,  _help: "name of site" }
	url: string & strings.MinRunes(2)
}
