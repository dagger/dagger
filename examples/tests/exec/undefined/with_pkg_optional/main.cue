package testing

import (
	"dagger.cloud/optional"
)

#dagger: {
	compute: [
		{
			do: "load",
			from: optional
		},
	]
}
