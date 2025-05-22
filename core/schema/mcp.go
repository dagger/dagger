package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type mcpSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &mcpSchema{}

func (s *mcpSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("__mcp", s.mcp).
			Doc("instantiates an MCP"),
	}.Install(s.srv)
	dagql.Fields[*core.MCP]{
		dagql.Func("__serve", s.serve).
			Doc("serve MCP"),
		dagql.Func("__setEnv", s.setEnv).
			Doc("set the environment to use"),
	}.Install(s.srv)
}

func (s *mcpSchema) mcp(ctx context.Context, parent *core.Query, _ struct{}) (*core.MCP, error) {
	// instantiate mcp with a nil env, to allow lazy loading via setenv
	var env *core.Env
	return core.NewMCP(parent, env), nil
}

func (s *mcpSchema) serve(ctx context.Context, mcp *core.MCP, _ struct{}) (*core.MCP, error) {
	return mcp, mcp.Serve(ctx, s.srv)
}

func (s *mcpSchema) setEnv(ctx context.Context, mcp *core.MCP, args struct {
	Env core.EnvID
}) (*core.MCP, error) {
	env, err := args.Env.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	return mcp.SetEnv(env.Self), nil
}
