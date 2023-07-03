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
