package testing

import (
	"dagger.io/nonoptional"
)

#dagger: {
	compute: [
		{
			do: "load",
			from: nonoptional
		},
	]
}
