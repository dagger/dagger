package testing

import (
	"dagger.io/def"
)

#dagger: {
	compute: [
		{
			do: "load",
			from: def
		},
	]
}
