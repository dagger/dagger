package attr

import (
	"strings"
)

#Site: {
	name: string @input(help="name of site")
	name: _ @input(ui=input,ios=button)
	url: string & strings.MinRunes(2)
}

foo: {
	bar: {
		moo: "cow"
	}
}
