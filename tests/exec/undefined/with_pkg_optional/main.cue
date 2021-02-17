package testing

import (
	"dagger.io/optional"
)

#dagger: {
	compute: [
		{
			do: "load",
			from: optional
		},
	]
}
