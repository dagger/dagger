package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type socketSchema struct{}

var _ SchemaResolvers = &socketSchema{}

func (s *socketSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Socket]{}.Install(srv)
}
