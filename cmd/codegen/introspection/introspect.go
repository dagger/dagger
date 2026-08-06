package introspection

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/cmd/codegen/internal/bootstrap"
)

// Introspect gets the Dagger Schema
func Introspect(ctx context.Context, dag *bootstrap.Client) (*Schema, string, error) {
	var introspectionResp Response
	err := dag.Do(ctx, &bootstrap.Request{
		Query:  Query,
		OpName: "IntrospectionQuery",
	}, &bootstrap.Response{
		Data: &introspectionResp,
	})
	if err != nil {
		return nil, "", fmt.Errorf("introspection query: %w", err)
	}

	return introspectionResp.Schema, introspectionResp.SchemaVersion, nil
}
