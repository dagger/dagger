package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type socketSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &socketSchema{}

func (s *socketSchema) Install() {
	dagql.Fields[*core.Socket]{}.Install(s.srv)
}
