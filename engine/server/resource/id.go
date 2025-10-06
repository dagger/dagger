package resource

import "github.com/dagger/dagger/dagql/call"

type ID struct {
	call.ID
	Optional bool
}
