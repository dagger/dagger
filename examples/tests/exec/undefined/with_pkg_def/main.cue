package testing

import (
	"dagger.cloud/def"
)

#dagger: {
	compute: [
		{
			do: "load",
			from: def
		},
	]
}
