package task

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"

	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("ClientNetwork", func() Task { return &clientNetwork{} })
}

type clientNetwork struct{}

func (t clientNetwork) Run(ctx context.Context, pctx *plancontext.Context, _ *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

	addr, err := v.Lookup("address").String()
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(addr)
	if err != nil {
		return nil, err
	}

	lg.Debug().Str("type", u.Scheme).Str("path", u.Path).Msg("loading local socket")

	if _, err := os.Stat(u.Path); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("path %q does not exist", u.Path)
	}

	var unix, npipe string

	switch u.Scheme {
	case "unix":
		unix = u.Path
	case "npipe":
		npipe = u.Path
	default:
		return nil, fmt.Errorf("invalid service type %q", u.Scheme)
	}

	connect := v.Lookup("connect")

	if !plancontext.IsServiceValue(connect) {
		return nil, fmt.Errorf("wrong type %q", connect.Kind())
	}

	service := pctx.Services.New(unix, npipe)

	return compiler.NewValue().FillFields(map[string]interface{}{
		"connect": service.MarshalCUE(),
	})
}
