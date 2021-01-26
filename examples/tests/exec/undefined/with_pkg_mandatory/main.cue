package testing

import (
	"dagger.cloud/nonoptional"
)

#dagger: {
	compute: [
		{
			do: "load",
			from: nonoptional
		},
	]
}
