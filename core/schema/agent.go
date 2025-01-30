package schema

import (
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type agentSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &agentSchema{}

func (s agentSchema) Install() {
	s.srv.SetMiddleware(core.AgentMiddleware{Server: s.srv})
}
