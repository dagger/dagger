package introspection

import (
	"context"
	"fmt"

	"dagger.io/dagger"
)

// Introspect gets the Dagger Schema
func Introspect(ctx context.Context, dag *dagger.Client) (*Schema, string, error) {
	var introspectionResp Response
	err := dag.Do(ctx, &dagger.Request{
		Query:  Query,
		OpName: "IntrospectionQuery",
	}, &dagger.Response{
		Data: &introspectionResp,
	})
	if err != nil {
		return nil, "", fmt.Errorf("introspection query: %w", err)
	}

	return introspectionResp.Schema, introspectionResp.SchemaVersion, nil
}
