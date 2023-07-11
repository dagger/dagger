package universe

import (
	"context"

	"dagger.io/dagger"
)

type Context interface {
	context.Context
	Client() *dagger.Client
}
