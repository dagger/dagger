package schema

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"go.opentelemetry.io/otel/trace"
)

type cloudSchema struct{}

var _ SchemaResolvers = &cloudSchema{}

func (s *cloudSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("cloud", s.cloud).
			Doc("Dagger Cloud configuration and state"),
	}.Install(srv)

	dagql.Fields[*core.Cloud]{
		dagql.Func("traceURL", s.traceURL).
			DoNotCache("This value changes every single run").
			Doc("The trace URL for the current session"),
	}.Install(srv)
}

func (s *cloudSchema) cloud(ctx context.Context, parent *core.Query, args struct{}) (*core.Cloud, error) {
	return &core.Cloud{}, nil
}

func (s *cloudSchema) traceURL(ctx context.Context, parent *core.Cloud, args struct{}) (string, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	md, err := query.MainClientCallerMetadata(ctx)
	if err != nil {
		return "", err
	}

	if md.CloudOrg == "" {
		return "", errors.New("no cloud organization configured; `dagger login` to configure your Dagger Cloud organization")
	}
	tid := trace.SpanContextFromContext(ctx).TraceID().String()
	return fmt.Sprintf("https://dagger.cloud/%s/traces/%s", md.CloudOrg, tid), nil
}
