package core

import (
	"context"

	"github.com/dagger/graphql"
	"github.com/vito/progrock"
)

type Context struct {
	context.Context

	ResolveParams graphql.ResolveParams

	// Vertex is a recorder for sending logs to the request's vertex in the
	// progress stream.
	Vertex *progrock.VertexRecorder
}

func (ctx *Context) Recorder() *progrock.Recorder {
	return progrock.RecorderFromContext(ctx.Context)
}

func (ctx *Context) WithRecorder(rec *progrock.Recorder) *Context {
	cp := *ctx
	cp.Context = progrock.RecorderToContext(cp.Context, rec)
	return &cp
}
