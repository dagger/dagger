package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type platformSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &platformSchema{}

func (s *platformSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("defaultPlatform", s.defaultPlatform).
			Doc(`The default platform of the engine.`),
	}.Install(s.srv)

	s.srv.InstallScalar(core.Platform{})
}

func (s *platformSchema) defaultPlatform(ctx context.Context, parent *core.Query, _ struct{}) (core.Platform, error) {
	return parent.Platform(), nil
}
