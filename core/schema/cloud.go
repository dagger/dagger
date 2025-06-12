package schema

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"go.opentelemetry.io/otel/trace"
)

type cloudSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &cloudSchema{}

func (s *cloudSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("cloud", s.cloud).
			Doc("Dagger Cloud configuration and state"),
	}.Install(s.srv)

	dagql.Fields[*core.Cloud]{
		dagql.Func("traceURL", s.traceURL).
			DoNotCache("").
			Doc("The URL for the Dagger Cloud instance associated with the current trace"),
	}.Install(s.srv)
}

func (s *cloudSchema) cloud(ctx context.Context, parent *core.Query, args struct{}) (*core.Cloud, error) {
	return &core.Cloud{}, nil
}

func (s *cloudSchema) traceURL(ctx context.Context, parent *core.Cloud, args struct{}) (string, error) {
	m, err := engine.ClientMetadataFromContext(ctx)

	if err != nil {
		return "", err
	}

	if m.CloudOrg == "" {
		return "", errors.New("no cloud organization configured; `dagger cloud login` to configure your Dagger Cloud organization")
	}
	tid := trace.SpanContextFromContext(ctx).TraceID().String()
	return fmt.Sprintf("https://dagger.cloud/%s/traces/%s", m.CloudOrg, tid), nil
}
